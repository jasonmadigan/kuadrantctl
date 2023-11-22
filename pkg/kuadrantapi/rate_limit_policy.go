package kuadrantapi

import (
	"github.com/getkin/kin-openapi/openapi3"
	kuadrantapiv1beta2 "github.com/kuadrant/kuadrant-operator/api/v1beta2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayapiv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"github.com/kuadrant/kuadrantctl/pkg/gatewayapi"
	"github.com/kuadrant/kuadrantctl/pkg/utils"
)

func RateLimitPolicyObjectMetaFromOAS(doc *openapi3.T) metav1.ObjectMeta {
	return gatewayapi.HTTPRouteObjectMetaFromOAS(doc)
}

func RateLimitPolicyLimitsFromOAS(doc *openapi3.T) map[string]kuadrantapiv1beta2.Limit {
	// Current implementation, one limit per operation
	// TODO(eguzki): consider about grouping operations in fewer RLP limits

	limits := make(map[string]kuadrantapiv1beta2.Limit)

	basePath, err := utils.BasePathFromOpenAPI(doc)
	if err != nil {
		panic(err)
	}

	// Paths
	for path, pathItem := range doc.Paths {
		kuadrantPathExtension, err := utils.NewKuadrantOASPathExtension(pathItem)
		if err != nil {
			panic(err)
		}

		pathEnabled := kuadrantPathExtension.IsEnabled()

		// Operations
		for verb, operation := range pathItem.Operations() {
			kuadrantOperationExtension, err := utils.NewKuadrantOASOperationExtension(operation)
			if err != nil {
				panic(err)
			}

			if !ptr.Deref(kuadrantOperationExtension.Enable, pathEnabled) {
				// not enabled for the operation
				//fmt.Printf("OUT not enabled: path: %s, method: %s\n", path, verb)
				continue
			}

			// default backendrefs at the path level
			rateLimit := kuadrantPathExtension.RateLimit
			if kuadrantOperationExtension.RateLimit != nil {
				rateLimit = kuadrantOperationExtension.RateLimit
			}

			if rateLimit == nil {
				// no rate limit defined for this operation
				//fmt.Printf("OUT no rate limit defined: path: %s, method: %s\n", path, verb)
				continue
			}

			limitName := utils.OpenAPIOperationName(path, verb, operation)

			limits[limitName] = kuadrantapiv1beta2.Limit{
				RouteSelectors: buildLimitRouteSelectors(basePath, path, pathItem, verb, operation),
				When:           rateLimit.When,
				Counters:       rateLimit.Counters,
				Rates:          rateLimit.Rates,
			}
		}
	}

	if len(limits) == 0 {
		return nil
	}

	return limits
}

func buildLimitRouteSelectors(basePath, path string, pathItem *openapi3.PathItem, verb string, op *openapi3.Operation) []kuadrantapiv1beta2.RouteSelector {
	match := utils.OpenAPIMatcherFromOASOperations(basePath, path, pathItem, verb, op)

	return []kuadrantapiv1beta2.RouteSelector{
		{
			Matches: []gatewayapiv1beta1.HTTPRouteMatch{match},
		},
	}
}
