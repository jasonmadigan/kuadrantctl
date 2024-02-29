package cmd

import (
	"context"
	"flag"
	"fmt"
	"time"

	operators "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	kuadrantoperator "github.com/kuadrant/kuadrant-operator/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kuadrant/kuadrantctl/pkg/utils"
)

var (
	installKubeConfig string
	// TODO(eastizle): namespace from command line param
	installNamespace string = "kuadrant-system"
)

const (
	KUADRANT_OPERATOR_VERSION string = "v0.4.1"
)

func installCommand() *cobra.Command {
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install Kuadrant",
		Long:  "The install command installs kuadrant in a OLM powered cluster",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Required to have controller-runtim config package read the kubeconfig arg
			err := flag.CommandLine.Parse([]string{"-kubeconfig", installKubeConfig})
			if err != nil {
				return err
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return installRun(cmd, args)
		},
	}

	// TODO(eastizle): add context flag to switch between kubeconfig contexts
	// It would require using config.GetConfigWithContext(context string) (*rest.Config, error)
	installCmd.PersistentFlags().StringVarP(&installKubeConfig, "kubeconfig", "", "", "Kubernetes configuration file")
	return installCmd
}

func installRun(cmd *cobra.Command, args []string) error {
	err := utils.SetupScheme()
	if err != nil {
		return err
	}

	configuration, err := config.GetConfig()
	if err != nil {
		return err
	}

	k8sClient, err := client.New(configuration, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return err
	}

	// Perform dependency checks right after the Kubernetes client is ready
	if err := performDependencyChecks(k8sClient); err != nil {
		// If the dependency checks fail, log the error and return it to halt the installation process
		logf.Log.Error(err, "Dependency checks failed")
		return err
	}

	// Continue with the rest of the installation process if all dependency checks pass
	err = deployKuadrantOperator(k8sClient)
	if err != nil {
		return err
	}

	err = createNamespace(k8sClient)
	if err != nil {
		return err
	}

	err = deployKuadrant(k8sClient)
	if err != nil {
		return err
	}

	return nil
}

func checkCRDExistence(k8sClient client.Client, crdName, documentationURL string) error {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	err := k8sClient.Get(context.Background(), types.NamespacedName{Name: crdName}, crd)
	if err != nil {
		logf.Log.Info(fmt.Sprintf("CRD %s not found. Please visit %s for installation instructions.", crdName, documentationURL))
		return fmt.Errorf("dependency CRD %s is not installed. Please follow the installation guide at %s", crdName, documentationURL)
	}
	return nil
}

func performDependencyChecks(k8sClient client.Client) error {
	var dependencyErrors []error

	// Perform each check and collect errors
	if err := checkCRDExistence(k8sClient, "gateways.networking.istio.io", "https://istio.io/latest/docs/setup/additional-setup/getting-started/"); err != nil {
		dependencyErrors = append(dependencyErrors, err)
	}
	if err := checkCRDExistence(k8sClient, "gatewayclasses.gateway.networking.k8s.io", "https://gateway-api.sigs.k8s.io/guides/#installing-gateway-api"); err != nil {
		dependencyErrors = append(dependencyErrors, err)
	}
	if err := checkCRDExistence(k8sClient, "certificates.cert-manager.io", "https://cert-manager.io/docs/installation/"); err != nil {
		dependencyErrors = append(dependencyErrors, err)
	}

	// If any errors were collected, return an aggregated error
	if len(dependencyErrors) > 0 {
		return fmt.Errorf("dependency checks failed: %+v", dependencyErrors)
	}

	// If no errors were collected, return nil to indicate success
	return nil
}

func waitForDeployments(k8sClient client.Client) error {
	retryInterval := time.Second * 5
	timeout := time.Minute * 3

	deploymentKeys := []types.NamespacedName{
		types.NamespacedName{Name: "authorino", Namespace: installNamespace},
		types.NamespacedName{Name: "limitador-limitador", Namespace: installNamespace},
	}

	for _, key := range deploymentKeys {
		immediate := true
		err := wait.PollUntilContextTimeout(context.Background(), retryInterval, timeout, immediate, func(ctx context.Context) (bool, error) {
			return utils.CheckDeploymentAvailable(ctx, k8sClient, key)
		})

		if err != nil {
			return err
		}
	}

	return nil
}

func deployKuadrantOperator(k8sClient client.Client) error {
	logf.Log.Info("Installing kuadrant operator", "version", KUADRANT_OPERATOR_VERSION)

	//apiVersion: operators.coreos.com/v1alpha1
	//kind: Subscription
	//metadata:
	//  name: my-kuadrant-operator
	//  namespace: operators
	//spec:
	//  channel: stable
	//  name: kuadrant-operator
	//  source: operatorhubio-catalog
	//  sourceNamespace: olm
	//
	subs := &operators.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "operators.coreos.com/v1alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "kuadrant-operator", Namespace: "operators"},
		Spec: &operators.SubscriptionSpec{
			Channel:                "stable",
			Package:                "kuadrant-operator",
			CatalogSource:          "operatorhubio-catalog",
			CatalogSourceNamespace: "olm",
			StartingCSV:            fmt.Sprintf("kuadrant-operator.%s", KUADRANT_OPERATOR_VERSION),
		},
	}

	err := utils.CreateOnlyK8SObject(k8sClient, subs)
	if err != nil {
		return err
	}

	var installPlanKey client.ObjectKey

	// Wait for the install process to be completed
	immediate := true
	logf.Log.Info("Waiting for the kuadrant operator installation")
	err = wait.PollUntilContextTimeout(context.Background(), time.Second*5, time.Minute*2, immediate, func(ctx context.Context) (bool, error) {
		existingSubs := &operators.Subscription{}
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(subs), existingSubs)
		if err != nil {
			if apierrors.IsNotFound(err) {
				logf.Log.Info("Subscription not available", "name", client.ObjectKeyFromObject(subs))
				return false, nil
			}
			return false, err
		}

		if existingSubs.Status.Install == nil || existingSubs.Status.Install.Name == "" {
			return false, nil
		}

		installPlanKey = client.ObjectKey{
			Name:      existingSubs.Status.Install.Name,
			Namespace: subs.Namespace,
		}

		return true, nil
	})

	if err != nil {
		return err
	}

	logf.Log.Info("Waiting for the install plan", "name", installPlanKey)
	err = wait.PollUntilContextTimeout(context.Background(), time.Second*5, time.Minute*2, immediate, func(ctx context.Context) (bool, error) {
		ip := &operators.InstallPlan{}
		err := k8sClient.Get(ctx, installPlanKey, ip)
		if err != nil {
			if apierrors.IsNotFound(err) {
				logf.Log.Info("Install plan not available", "name", installPlanKey)
				return false, nil
			}
			return false, err
		}

		if ip.Status.Phase != operators.InstallPlanPhaseComplete {
			logf.Log.Info("Install plan not ready", "phase", ip.Status.Phase)
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return err
	}

	return nil
}

func deployKuadrant(k8sClient client.Client) error {
	kuadrant := &kuadrantoperator.Kuadrant{
		TypeMeta:   metav1.TypeMeta{APIVersion: "kuadrant.io/v1beta1", Kind: "Kuadrant"},
		ObjectMeta: metav1.ObjectMeta{Name: "kuadrant", Namespace: installNamespace},
		Spec:       kuadrantoperator.KuadrantSpec{},
	}

	err := utils.CreateOnlyK8SObject(k8sClient, kuadrant)
	if err != nil {
		return err
	}

	return waitForDeployments(k8sClient)
}

func createNamespace(k8sClient client.Client) error {
	nsObj := &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: installNamespace},
	}
	logf.Log.Info("Creating kuadrant namespace", "name", installNamespace)
	err := utils.CreateOnlyK8SObject(k8sClient, nsObj)
	if err != nil {
		return err
	}

	retryInterval := time.Second * 2
	timeout := time.Second * 20
	immediate := true
	return wait.PollUntilContextTimeout(context.Background(), retryInterval, timeout, immediate, func(ctx context.Context) (bool, error) {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: installNamespace}, &corev1.Namespace{})
		if err != nil && apierrors.IsNotFound(err) {
			return false, nil
		}
		return true, err
	})
}
