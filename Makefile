SHELL := /bin/bash

MKFILE_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
PROJECT_PATH := $(patsubst %/,%,$(dir $(MKFILE_PATH)))
GO ?= go
KUADRANT_NAMESPACE=kuadrant-system

include utils.mk

all: help

.PHONY : help
help: Makefile
	@sed -n 's/^##//p' $<

# Kind tool
KIND = $(PROJECT_PATH)/bin/kind
KIND_CLUSTER_NAME = kuadrant-local
$(KIND):
	$(call go-get-tool,$(KIND),sigs.k8s.io/kind@v0.11.1)

.PHONY : kind
kind: $(KIND)

# istioctl tool
ISTIOCTL=$(PROJECT_PATH)/bin/istioctl
ISTIOVERSION = 1.12.1
$(ISTIOCTL):
	mkdir -p $(PROJECT_PATH)/bin
	$(eval TMP := $(shell mktemp -d))
	cd $(TMP); curl -sSL https://istio.io/downloadIstio | ISTIO_VERSION=$(ISTIOVERSION) sh -
	cp $(TMP)/istio-$(ISTIOVERSION)/bin/istioctl ${ISTIOCTL}
	-rm -rf $(TMP)

.PHONY: istioctl
istioctl: $(ISTIOCTL)

# Ginkgo tool
GINKGO = $(PROJECT_PATH)/bin/ginkgo
$(GINKGO):
	$(call go-get-tool,$(GINKGO),github.com/onsi/ginkgo/ginkgo@v1.16.4)

## test: Run unit tests
.PHONY : test
test: fmt vet $(GINKGO)
	# huffle both the order in which specs within a suite run, and the order in which different suites run
	# You can always rerun a given ordering later by passing the --seed flag a matching seed.
	$(GINKGO) --randomizeAllSpecs --randomizeSuites -v -progress --trace --cover ./...

## install: Build and install kuadrantctl binary ($GOBIN or GOPATH/bin)
.PHONY : install
install: fmt vet
	$(GO) install

.PHONY : fmt
fmt:
	$(GO) fmt ./...

.PHONY : vet
vet:
	$(GO) vet ./...

# Generates istio manifests with patches.
.PHONY: generate-istio-manifests
generate-istio-manifests: istioctl
	$(ISTIOCTL) manifest generate --set profile=minimal --set values.gateways.istio-ingressgateway.autoscaleEnabled=false --set values.pilot.autoscaleEnabled=false --set values.global.istioNamespace=kuadrant-system -f istiomanifests/patches/istio-externalProvider.yaml -o istiomanifests/autogenerated

.PHONY: istio-manifest-update-test
istio-manifest-update-test: generate-istio-manifests
	git diff --exit-code ./istiomanifests/autogenerated
	[ -z "$$(git ls-files --other --exclude-standard --directory --no-empty-directory ./istiomanifests/autogenerated)" ]

# Generates kuadrant manifests.
KUADRANTVERSION=v0.2.0
KUADRANT_CONTROLLER_IMAGE=quay.io/3scale/kuadrant-controller:$(KUADRANTVERSION)
.PHONY: generate-kuadrant-manifests
generate-kuadrant-manifests:
	$(eval TMP := $(shell mktemp -d))
	cd $(TMP); git clone --depth 1 --branch $(KUADRANTVERSION) https://github.com/kuadrant/kuadrant-controller.git
	cd $(TMP)/kuadrant-controller; make kustomize
	cd $(TMP)/kuadrant-controller/config/manager; $(TMP)/kuadrant-controller/bin/kustomize edit set image controller=${KUADRANT_CONTROLLER_IMAGE}
	cd $(TMP)/kuadrant-controller/config/default; $(TMP)/kuadrant-controller/bin/kustomize edit set namespace $(KUADRANT_NAMESPACE)
	cd $(TMP)/kuadrant-controller; bin/kustomize build config/default -o $(PROJECT_PATH)/kuadrantmanifests/autogenerated/kuadrant.yaml
	-rm -rf $(TMP)

.PHONY: kuadrant-manifest-update-test
kuadrant-manifest-update-test: generate-kuadrant-manifests
	git diff --exit-code ./kuadrantmanifests/autogenerated
	[ -z "$$(git ls-files --other --exclude-standard --directory --no-empty-directory ./kuadrantmanifests/autogenerated)" ]

# Generates limitador manifests.
LIMITADOR_OPERATOR_VERSION=v0.2.0
LIMITADOR_OPERATOR_IMAGE=quay.io/3scale/limitador-operator:$(LIMITADOR_OPERATOR_VERSION)
.PHONY: generate-limitador-operator-manifests
generate-limitador-operator-manifests:
	$(eval TMP := $(shell mktemp -d))
	cd $(TMP); git clone --depth 1 --branch $(LIMITADOR_OPERATOR_VERSION) https://github.com/kuadrant/limitador-operator.git
	cd $(TMP)/limitador-operator; make kustomize
	cd $(TMP)/limitador-operator/config/manager; $(TMP)/limitador-operator/bin/kustomize edit set image controller=$(LIMITADOR_OPERATOR_IMAGE)
	cd $(TMP)/limitador-operator/config/default; $(TMP)/limitador-operator/bin/kustomize edit set namespace $(KUADRANT_NAMESPACE)
	cd $(TMP)/limitador-operator; bin/kustomize build config/default -o $(PROJECT_PATH)/limitadormanifests/autogenerated/limitador-operator.yaml
	-rm -rf $(TMP)

.PHONY: limitador-operator-manifest-update-test
limitador-operator-manifest-update-test: generate-limitador-operator-manifests
	git diff --exit-code ./limitadormanifests/autogenerated
	[ -z "$$(git ls-files --other --exclude-standard --directory --no-empty-directory ./limitadormanifests/autogenerated)" ]

# Generates authorino operator manifests.
AUTHORINO_OPERATOR_VERSION=v0.1.0
.PHONY: generate-authorino-operator-manifests
generate-authorino-operator-manifests:
	curl -sSf https://raw.githubusercontent.com/Kuadrant/authorino-operator/$(AUTHORINO_OPERATOR_VERSION)/config/deploy/manifests.yaml > $(PROJECT_PATH)/authorinomanifests/autogenerated/authorino-operator.yaml

.PHONY: authorino-manifest-update-test
authorino-operator-manifest-update-test: generate-authorino-operator-manifests
	git diff --exit-code ./authorinomanifests/autogenerated
	[ -z "$$(git ls-files --other --exclude-standard --directory --no-empty-directory ./authorinomanifests/autogenerated)" ]

.PHONY : cluster-cleanup
cluster-cleanup: $(KIND)
	$(KIND) delete cluster --name $(KIND_CLUSTER_NAME)

.PHONY : cluster-setup
cluster-setup: $(KIND) cluster-cleanup
	$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --config utils/kind/cluster.yaml

GOLANGCI-LINT=$(PROJECT_PATH)/bin/golangci-lint
$(GOLANGCI-LINT):
	mkdir -p $(PROJECT_PATH)/bin
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(PROJECT_PATH)/bin v1.41.1

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI-LINT)

.PHONY: run-lint
run-lint: $(GOLANGCI-LINT)
	$(GOLANGCI-LINT) run --timeout 2m

