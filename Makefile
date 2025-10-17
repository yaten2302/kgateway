# imports should be after the set up flags so are lower

# https://www.gnu.org/software/make/manual/html_node/Special-Variables.html#Special-Variables
.DEFAULT_GOAL := help
SHELL := /bin/bash

#----------------------------------------------------------------------------------
# Help
#----------------------------------------------------------------------------------
# Our Makefile is quite large, and hard to reason through
# `make help` can be used to self-document targets
# To update a target to be self-documenting (and appear with the `help` command),
# place a comment after the target that is prefixed by `##`. For example:
#	custom-target: ## comment that will appear in the documentation when running `make help`
#
# **NOTE TO DEVELOPERS**
# As you encounter make targets that are frequently used, please make them self-documenting
.PHONY: help
help: NAME_COLUMN_WIDTH=35
help: LINE_COLUMN_WIDTH=5
help: ## Output the self-documenting make targets
	@grep -hnE '^[%a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = "[:]|(## )"}; {printf "\033[36mL%-$(LINE_COLUMN_WIDTH)s%-$(NAME_COLUMN_WIDTH)s\033[0m %s\n", $$1, $$2, $$4}'

#----------------------------------------------------------------------------------
# Base
#----------------------------------------------------------------------------------

ROOTDIR := $(shell pwd)
OUTPUT_DIR ?= $(ROOTDIR)/_output

export IMAGE_REGISTRY ?= ghcr.io/kgateway-dev

# Kind of a hack to make sure _output exists
z := $(shell mkdir -p $(OUTPUT_DIR))

BUILDX_BUILD := docker buildx build -q

# A semver resembling 1.0.1-dev. Most calling GHA jobs customize this. Exported for use in goreleaser.yaml.
VERSION ?= 1.0.1-dev
export VERSION

SOURCES := $(shell find . -name "*.go" | grep -v test.go)

# Note: When bumping this version, update the version in pkg/validator/validator.go as well.
export ENVOY_IMAGE ?= quay.io/solo-io/envoy-gloo:1.35.2-patch4
export LDFLAGS := -X 'github.com/kgateway-dev/kgateway/v2/internal/version.Version=$(VERSION)'
export GCFLAGS ?=

UNAME_M := $(shell uname -m)
# if `GO_ARCH` is set, then it will keep its value. Else, it will be changed based off the machine's host architecture.
# if the machines architecture is set to arm64 then we want to set the appropriate values, else we only support amd64
IS_ARM_MACHINE := $(or	$(filter $(UNAME_M), arm64), $(filter $(UNAME_M), aarch64))
ifneq ($(IS_ARM_MACHINE), )
	ifneq ($(GOARCH), amd64)
		GOARCH := arm64
	endif
else
	# currently we only support arm64 and amd64 as a GOARCH option.
	ifneq ($(GOARCH), arm64)
		GOARCH := amd64
	endif
endif

PLATFORM := --platform=linux/$(GOARCH)
PLATFORM_MULTIARCH := $(PLATFORM)
LOAD_OR_PUSH := --load
ifeq ($(MULTIARCH), true)
	PLATFORM_MULTIARCH := --platform=linux/amd64,linux/arm64
	LOAD_OR_PUSH :=

	ifeq ($(MULTIARCH_PUSH), true)
		LOAD_OR_PUSH := --push
	endif
endif

GOOS ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')

GO_BUILD_FLAGS := GO111MODULE=on CGO_ENABLED=0 GOARCH=$(GOARCH)

TEST_ASSET_DIR ?= $(ROOTDIR)/_test

# This is the location where assets are placed after a test failure
# This is used by our e2e tests to emit information about the running instance of kgateway
BUG_REPORT_DIR := $(TEST_ASSET_DIR)/bug_report
$(BUG_REPORT_DIR):
	mkdir -p $(BUG_REPORT_DIR)

# Base Alpine image used for all containers. Exported for use in goreleaser.yaml.
export ALPINE_BASE_IMAGE ?= alpine:3.17.6

#----------------------------------------------------------------------------------
# Macros
#----------------------------------------------------------------------------------

# This macro takes a relative path as its only argument and returns all the files
# in the tree rooted at that directory that match the given criteria.
get_sources = $(shell find $(1) -name "*.go" | grep -v test | grep -v generated.go | grep -v mock_)

#----------------------------------------------------------------------------------
# Repo setup
#----------------------------------------------------------------------------------

GOIMPORTS ?= go tool goimports

.PHONY: init-git-hooks
init-git-hooks:  ## Use the tracked version of Git hooks from this repo
	git config core.hooksPath .githooks

.PHONY: fmt
fmt:  ## Format the code with goimports
	$(GOIMPORTS) -local "github.com/kgateway-dev/kgateway/v2/"  -w $(shell ls -d */ | grep -v vendor)

.PHONY: fmt-changed
fmt-changed:  ## Format the code with goimports
	git diff --name-only | grep '.*.go$$' | xargs -- $(GOIMPORTS) -w

# must be a separate target so that make waits for it to complete before moving on
.PHONY: mod-download
mod-download:  ## Download the dependencies
	go mod download all

.PHONY: mod-tidy-nested
mod-tidy-nested:  ## Tidy go mod files in nested modules
	@echo "Tidying hack/utils/applier..." && cd hack/utils/applier && go mod tidy

.PHONY: mod-tidy
mod-tidy: mod-download mod-tidy-nested ## Tidy the go mod file
	go mod tidy

#----------------------------------------------------------------------------
# Analyze
#----------------------------------------------------------------------------

YQ ?= go tool yq
GO_VERSION := $(shell cat go.mod | grep -E '^go' | awk '{print $$2}')
GOTOOLCHAIN ?= go$(GO_VERSION)

GOLANGCI_LINT ?= go tool golangci-lint
ANALYZE_ARGS ?= --fix --verbose
.PHONY: analyze
analyze:  ## Run golangci-lint. Override options with ANALYZE_ARGS.
	GOTOOLCHAIN=$(GOTOOLCHAIN) $(GOLANGCI_LINT) run $(ANALYZE_ARGS) ./...

#----------------------------------------------------------------------------------
# Ginkgo Tests
#----------------------------------------------------------------------------------

FLAKE_ATTEMPTS ?= 3
GINKGO_VERSION ?= $(shell echo $(shell go list -m github.com/onsi/ginkgo/v2) | cut -d' ' -f2)
GINKGO_ENV ?= ACK_GINKGO_RC=true ACK_GINKGO_DEPRECATIONS=$(GINKGO_VERSION)

GINKGO_FLAGS ?= -tags=purego --trace -progress -race --fail-fast -fail-on-pending --randomize-all --compilers=5 --flake-attempts=$(FLAKE_ATTEMPTS)
GINKGO_REPORT_FLAGS ?= --json-report=test-report.json --junit-report=junit.xml -output-dir=$(OUTPUT_DIR)
GINKGO_COVERAGE_FLAGS ?= --cover --covermode=atomic --coverprofile=coverage.cov
TEST_PKG ?= ./... # Default to run all tests except e2e tests

# This is a way for a user executing `make test` to be able to provide flags which we do not include by default
# For example, you may want to run tests multiple times, or with various timeouts
GINKGO_USER_FLAGS ?=
GINKGO ?= go tool ginkgo

.PHONY: test
test: ## Run all tests, or only run the test package at {TEST_PKG} if it is specified
	$(GINKGO_ENV) $(GINKGO) -ldflags='$(LDFLAGS)' \
		$(GINKGO_FLAGS) $(GINKGO_REPORT_FLAGS) $(GINKGO_USER_FLAGS) \
		$(TEST_PKG)

# To run only e2e tests, we restrict to ./test/kubernetes/e2e/tests. We say
# '-tags=e2e' because untagged files contain unit tests cases, not e2e test
# cases, so we have to allow `go` to see our e2e tests. Someone might forget to
# label a new e2e test case with `//go:build e2e`, in which case `make unit`
# will error because there is no kind cluster.
#
# This build-tag approach makes unit tests run faster since e2e tests are not
# compiled, but it might be better to set an environment variable `E2E=true`
# and have end-to-end test cases report that they were skipped if it's not
# truthy. As it stands, a developer who runs `make unit` or `go test ./...`
# will still have e2e tests run by Github Actions once they publish a pull
# request.
.PHONY: e2e-test
e2e-test: TEST_PKG = ./test/kubernetes/e2e/tests
e2e-test: ## Run only e2e tests, and only run the test package at {TEST_PKG} if it is specified
	@$(MAKE) --no-print-directory go-test TEST_TAG=e2e TEST_PKG=$(TEST_PKG)


# https://go.dev/blog/cover#heat-maps
.PHONY: test-with-coverage
test-with-coverage: GINKGO_FLAGS += $(GINKGO_COVERAGE_FLAGS)
test-with-coverage: test
	go tool cover -html $(OUTPUT_DIR)/coverage.cov

#----------------------------------------------------------------------------------
# Env test
#----------------------------------------------------------------------------------

ENVTEST_K8S_VERSION = 1.23
ENVTEST ?= go tool setup-envtest

.PHONY: envtest-path
envtest-path: ## Set the envtest path
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path --arch=amd64

#----------------------------------------------------------------------------------
# Go Tests
#----------------------------------------------------------------------------------

# Fix for macOS linker warning with race detector on arm64 (which still warns
# you that -ld_classic is deprecated, but that's better than broken race
# condition detection)
# See: https://github.com/golang/go/issues/61229
GO_TEST_ENV ?=
ifeq ($(GOOS), darwin)
ifeq ($(GOARCH), arm64)
	override GO_TEST_ENV := CGO_LDFLAGS="-Wl,-ld_classic"
endif
endif

# Testing flags: https://pkg.go.dev/cmd/go#hdr-Testing_flags
# The default timeout for a suite is 10 minutes, but this can be overridden by setting the -timeout flag. Currently set
# to 25 minutes based on the time it takes to run the longest test setup (kgateway_test).
GO_TEST_ARGS ?= -timeout=25m -cpu=4 -race -outputdir=$(OUTPUT_DIR)
GO_TEST_COVERAGE_ARGS ?= --cover --covermode=atomic --coverprofile=cover.out
GO_TEST_COVERAGE ?= go tool github.com/vladopajic/go-test-coverage/v2

# This is a way for a user executing `make go-test` to be able to provide args which we do not include by default
# For example, you may want to run tests multiple times, or with various timeouts
GO_TEST_USER_ARGS ?=

.PHONY: go-test
go-test: ## Run all tests, or only run the test package at {TEST_PKG} if it is specified
go-test: reset-bug-report
	$(GO_TEST_ENV) go test -ldflags='$(LDFLAGS)' $(if $(TEST_TAG),-tags=$(TEST_TAG)) $(GO_TEST_ARGS) $(GO_TEST_USER_ARGS) $(TEST_PKG)

# https://go.dev/blog/cover#heat-maps
.PHONY: go-test-with-coverage
go-test-with-coverage: GO_TEST_ARGS += $(GO_TEST_COVERAGE_ARGS)
go-test-with-coverage: go-test

# https://go.dev/blog/cover#heat-maps
.PHONY: unit-with-coverage
unit-with-coverage:
	@$(MAKE) --no-print-directory unit GO_TEST_ARGS="$(GO_TEST_ARGS) $(GO_TEST_COVERAGE_ARGS)"

.PHONY: unit
unit: ## Run all unit tests (excludes e2e tests)
	@echo "Running unit tests (excluding e2e)..."
	@$(MAKE) --no-print-directory go-test TEST_TAG=""

.PHONY: validate-test-coverage
validate-test-coverage: ## Validate the test coverage
	$(GO_TEST_COVERAGE) --config=./test_coverage.yml

# https://go.dev/blog/cover#heat-maps
.PHONY: view-test-coverage
view-test-coverage:
	go tool cover -html $(OUTPUT_DIR)/cover.out

#----------------------------------------------------------------------------------
# Clean
#----------------------------------------------------------------------------------

# Important to clean before pushing new releases. Dockerfiles and binaries may not update properly
.PHONY: clean
clean:
	rm -rf _output
	rm -rf _test
	git clean -f -X install

# Clean generated code
# see hack/generate.sh for source of truth of dirs to clean
.PHONY: clean-gen
clean-gen:
	rm -rf api/applyconfiguration
	rm -rf pkg/generated/openapi
	rm -rf pkg/client
	rm -f install/helm/kgateway-crds/templates/gateway.kgateway.dev_*.yaml

.PHONY: clean-tests
clean-tests:
	find * -type f -name '*.test' -exec rm {} \;
	find * -type f -name '*.cov' -exec rm {} \;
	find * -type f -name 'junit*.xml' -exec rm {} \;

# NB: 'reset-bug-report: clean-bug-report $(BUG_REPORT_DIR)' would be a subtle
# bug since we would never run 'mkdir' if the directory already existed.
.PHONY: reset-bug-report
reset-bug-report: clean-bug-report
	@$(MAKE) --no-print-directory $(BUG_REPORT_DIR)

.PHONY: clean-bug-report
clean-bug-report:
	rm -rf $(BUG_REPORT_DIR)

#----------------------------------------------------------------------------------
# Generated Code
#----------------------------------------------------------------------------------

.PHONY: verify
verify: generate-all  ## Verify that generated code is up to date
	git diff -U3 --exit-code

.PHONY: generate-all
generate-all: generated-code

# Generates all required code, cleaning and formatting as well; this target is executed in CI
.PHONY: generated-code
generated-code: clean-gen go-generate-all mod-tidy
generated-code: generate-licenses
generated-code: fmt

.PHONY: go-generate-all
go-generate-all: go-generate-apis go-generate-mocks

.PHONY: go-generate-apis
go-generate-apis: ## Run all go generate directives in the repo, including codegen for protos, mockgen, and more
	GO111MODULE=on go generate ./hack/...

.PHONY: go-generate-mocks
go-generate-mocks: ## Runs all generate directives for mockgen in the repo
	GO111MODULE=on go generate -run="mockgen" ./...

.PHONY: generate-licenses
generate-licenses: ## Generate the licenses for the project
	GO111MODULE=on go run hack/utils/oss_compliance/oss_compliance.go osagen -c "GNU General Public License v2.0,GNU General Public License v3.0,GNU Lesser General Public License v2.1,GNU Lesser General Public License v3.0,GNU Affero General Public License v3.0"
	GO111MODULE=on go run hack/utils/oss_compliance/oss_compliance.go osagen -s "Mozilla Public License 2.0,GNU General Public License v2.0,GNU General Public License v3.0,GNU Lesser General Public License v2.1,GNU Lesser General Public License v3.0,GNU Affero General Public License v3.0"> hack/utils/oss_compliance/osa_provided.md
	GO111MODULE=on go run hack/utils/oss_compliance/oss_compliance.go osagen -i "Mozilla Public License 2.0"> hack/utils/oss_compliance/osa_included.md

#----------------------------------------------------------------------------------
# Controller
#----------------------------------------------------------------------------------

K8S_GATEWAY_SOURCES=$(call get_sources,cmd/kgateway internal/kgateway pkg/ api/)
CONTROLLER_OUTPUT_DIR=$(OUTPUT_DIR)/internal/kgateway
export CONTROLLER_IMAGE_REPO ?= kgateway

# We include the files in K8S_GATEWAY_SOURCES as dependencies to the kgateway build
# so changes in those directories cause the make target to rebuild
$(CONTROLLER_OUTPUT_DIR)/kgateway-linux-$(GOARCH): $(K8S_GATEWAY_SOURCES)
	$(GO_BUILD_FLAGS) GOOS=linux go build -ldflags='$(LDFLAGS)' -gcflags='$(GCFLAGS)' -o $@ ./cmd/kgateway/...

.PHONY: kgateway
kgateway: $(CONTROLLER_OUTPUT_DIR)/kgateway-linux-$(GOARCH)

$(CONTROLLER_OUTPUT_DIR)/Dockerfile: cmd/kgateway/Dockerfile
	cp $< $@

$(CONTROLLER_OUTPUT_DIR)/.docker-stamp-$(VERSION)-$(GOARCH): $(CONTROLLER_OUTPUT_DIR)/kgateway-linux-$(GOARCH) $(CONTROLLER_OUTPUT_DIR)/Dockerfile
	$(BUILDX_BUILD) --load $(PLATFORM) $(CONTROLLER_OUTPUT_DIR) -f $(CONTROLLER_OUTPUT_DIR)/Dockerfile \
		--build-arg GOARCH=$(GOARCH) \
		--build-arg ENVOY_IMAGE=$(ENVOY_IMAGE) \
		-t $(IMAGE_REGISTRY)/$(CONTROLLER_IMAGE_REPO):$(VERSION)
	@touch $@

.PHONY: kgateway-docker
kgateway-docker: $(CONTROLLER_OUTPUT_DIR)/.docker-stamp-$(VERSION)-$(GOARCH)

#----------------------------------------------------------------------------------
# SDS Server - gRPC server for serving Secret Discovery Service config
#----------------------------------------------------------------------------------

SDS_DIR=internal/sds
SDS_SOURCES=$(call get_sources,$(SDS_DIR))
SDS_OUTPUT_DIR=$(OUTPUT_DIR)/$(SDS_DIR)
export SDS_IMAGE_REPO ?= sds

$(SDS_OUTPUT_DIR)/sds-linux-$(GOARCH): $(SDS_SOURCES)
	$(GO_BUILD_FLAGS) GOOS=linux go build -ldflags='$(LDFLAGS)' -gcflags='$(GCFLAGS)' -o $@ ./cmd/sds/...

.PHONY: sds
sds: $(SDS_OUTPUT_DIR)/sds-linux-$(GOARCH)

$(SDS_OUTPUT_DIR)/Dockerfile.sds: cmd/sds/Dockerfile
	cp $< $@

$(SDS_OUTPUT_DIR)/.docker-stamp-$(VERSION)-$(GOARCH): $(SDS_OUTPUT_DIR)/sds-linux-$(GOARCH) $(SDS_OUTPUT_DIR)/Dockerfile.sds
	$(BUILDX_BUILD) --load $(PLATFORM) $(SDS_OUTPUT_DIR) -f $(SDS_OUTPUT_DIR)/Dockerfile.sds \
		--build-arg GOARCH=$(GOARCH) \
		--build-arg BASE_IMAGE=$(ALPINE_BASE_IMAGE) \
		-t $(IMAGE_REGISTRY)/$(SDS_IMAGE_REPO):$(VERSION)
	@touch $@

.PHONY: sds-docker
sds-docker: $(SDS_OUTPUT_DIR)/.docker-stamp-$(VERSION)-$(GOARCH)

#----------------------------------------------------------------------------------
# Envoy init (BASE/SIDECAR)
#----------------------------------------------------------------------------------

ENVOYINIT_DIR=cmd/envoyinit
ENVOYINIT_SOURCES=$(call get_sources,$(ENVOYINIT_DIR))
ENVOYINIT_OUTPUT_DIR=$(OUTPUT_DIR)/$(ENVOYINIT_DIR)
export ENVOYINIT_IMAGE_REPO ?= envoy-wrapper

$(ENVOYINIT_OUTPUT_DIR)/envoyinit-linux-$(GOARCH): $(ENVOYINIT_SOURCES)
	$(GO_BUILD_FLAGS) GOOS=linux go build -ldflags='$(LDFLAGS)' -gcflags='$(GCFLAGS)' -o $@ ./cmd/envoyinit/...

.PHONY: envoyinit
envoyinit: $(ENVOYINIT_OUTPUT_DIR)/envoyinit-linux-$(GOARCH)

# TODO(nfuden) cheat the process for now with -r but try to find a cleaner method
# Allow override of Dockerfile for local development
ENVOYINIT_DOCKERFILE ?= cmd/envoyinit/Dockerfile
$(ENVOYINIT_OUTPUT_DIR)/Dockerfile.envoyinit: $(ENVOYINIT_DOCKERFILE)
	@if [ "$(ENVOYINIT_DOCKERFILE)" = "cmd/envoyinit/Dockerfile" ]; then \
		cp -r internal/envoyinit/rustformations $(ENVOYINIT_OUTPUT_DIR); \
	fi
	cp $< $@

$(ENVOYINIT_OUTPUT_DIR)/docker-entrypoint.sh: cmd/envoyinit/docker-entrypoint.sh
	cp $< $@

$(ENVOYINIT_OUTPUT_DIR)/.docker-stamp-$(VERSION)-$(GOARCH): $(ENVOYINIT_OUTPUT_DIR)/envoyinit-linux-$(GOARCH) $(ENVOYINIT_OUTPUT_DIR)/Dockerfile.envoyinit $(ENVOYINIT_OUTPUT_DIR)/docker-entrypoint.sh
	$(BUILDX_BUILD) --load $(PLATFORM) $(ENVOYINIT_OUTPUT_DIR) -f $(ENVOYINIT_OUTPUT_DIR)/Dockerfile.envoyinit \
		--build-arg GOARCH=$(GOARCH) \
		--build-arg ENVOY_IMAGE=$(ENVOY_IMAGE) \
		-t $(IMAGE_REGISTRY)/$(ENVOYINIT_IMAGE_REPO):$(VERSION)
	@touch $@

.PHONY: envoy-wrapper-docker
envoy-wrapper-docker: $(ENVOYINIT_OUTPUT_DIR)/.docker-stamp-$(VERSION)-$(GOARCH)

#----------------------------------------------------------------------------------
# Helm
#----------------------------------------------------------------------------------

HELM ?= go tool helm
HELM_PACKAGE_ARGS ?= --version $(VERSION)
HELM_CHART_DIR=install/helm/kgateway
HELM_CHART_DIR_CRD=install/helm/kgateway-crds

.PHONY: package-kgateway-charts
package-kgateway-charts: package-kgateway-chart package-kgateway-crd-chart ## Package the kgateway charts

.PHONY: package-kgateway-chart
package-kgateway-chart: ## Package the kgateway charts
	mkdir -p $(TEST_ASSET_DIR); \
	$(HELM) package $(HELM_PACKAGE_ARGS) --destination $(TEST_ASSET_DIR) $(HELM_CHART_DIR); \
	$(HELM) repo index $(TEST_ASSET_DIR);

.PHONY: package-kgateway-crd-chart
package-kgateway-crd-chart: ## Package the kgateway crd chart
	mkdir -p $(TEST_ASSET_DIR); \
	$(HELM) package $(HELM_PACKAGE_ARGS) --destination $(TEST_ASSET_DIR) $(HELM_CHART_DIR_CRD); \
	$(HELM) repo index $(TEST_ASSET_DIR);

.PHONY: release-charts
release-charts: package-kgateway-charts ## Release the kgateway charts
	$(HELM) push $(TEST_ASSET_DIR)/kgateway-$(VERSION).tgz oci://$(IMAGE_REGISTRY)/charts
	$(HELM) push $(TEST_ASSET_DIR)/kgateway-crds-$(VERSION).tgz oci://$(IMAGE_REGISTRY)/charts

.PHONY: deploy-kgateway-crd-chart
deploy-kgateway-crd-chart: ## Deploy the kgateway crd chart
	$(HELM) upgrade --install kgateway-crds $(TEST_ASSET_DIR)/kgateway-crds-$(VERSION).tgz --namespace $(INSTALL_NAMESPACE) --create-namespace

HELM_ADDITIONAL_VALUES ?= hack/helm/dev.yaml
.PHONY: deploy-kgateway-chart
deploy-kgateway-chart: ## Deploy the kgateway chart
	$(HELM) upgrade --install kgateway $(TEST_ASSET_DIR)/kgateway-$(VERSION).tgz \
	--namespace $(INSTALL_NAMESPACE) --create-namespace \
	--set image.registry=$(IMAGE_REGISTRY) \
	--set image.tag=$(VERSION) \
	-f $(HELM_ADDITIONAL_VALUES)

.PHONY: lint-kgateway-charts
lint-kgateway-charts: ## Lint the kgateway charts
	$(HELM) lint $(HELM_CHART_DIR)
	$(HELM) lint $(HELM_CHART_DIR_CRD)

#----------------------------------------------------------------------------------
# Release
#----------------------------------------------------------------------------------

GORELEASER ?= go tool github.com/goreleaser/goreleaser/v2
GORELEASER_ARGS ?= --snapshot --clean
GORELEASER_TIMEOUT ?= 60m
GORELEASER_CURRENT_TAG ?= $(VERSION)

.PHONY: release
release: ## Create a release using goreleaser
	GORELEASER_CURRENT_TAG=$(GORELEASER_CURRENT_TAG) $(GORELEASER) release $(GORELEASER_ARGS) --timeout $(GORELEASER_TIMEOUT)

#----------------------------------------------------------------------------------
# Development
#----------------------------------------------------------------------------------

KIND ?= go tool kind
CLUSTER_NAME ?= kind
INSTALL_NAMESPACE ?= kgateway-system

# The version of the Node Docker image to use for booting the kind cluster: https://hub.docker.com/r/kindest/node/tags
# This version should stay in sync with `hack/kind/setup-kind.sh`.
CLUSTER_NODE_VERSION ?= kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a

.PHONY: kind-create
kind-create: ## Create a KinD cluster
	$(KIND) get clusters | grep $(CLUSTER_NAME) || $(KIND) create cluster --name $(CLUSTER_NAME) --image kindest/node:$(CLUSTER_NODE_VERSION)

CONFORMANCE_CHANNEL ?= experimental
CONFORMANCE_VERSION ?= v1.4.0
.PHONY: gw-api-crds
gw-api-crds: ## Install the Gateway API CRDs. HACK: Use SSA to avoid the issue with the CRD annotations being too long.
ifeq ($(CONFORMANCE_CHANNEL), standard)
	kubectl apply --server-side --kustomize "https://github.com/kubernetes-sigs/gateway-api/config/crd?ref=$(CONFORMANCE_VERSION)"
else
	kubectl apply --server-side --kustomize "https://github.com/kubernetes-sigs/gateway-api/config/crd/$(CONFORMANCE_CHANNEL)?ref=$(CONFORMANCE_VERSION)"
endif

# The version of the k8s gateway api inference extension CRDs to install.
# Managed by `make bump-gie`.
GIE_CRD_VERSION ?= 51485db93d63bfa2f9264460798671b72bdf9f5d

.PHONY: gie-crds
gie-crds: ## Install the Gateway API Inference Extension CRDs
	kubectl apply --kustomize "https://github.com/kubernetes-sigs/gateway-api-inference-extension/config/crd?ref=$(GIE_CRD_VERSION)"

.PHONY: kind-metallb
metallb: ## Install the MetalLB load balancer
	./hack/kind/setup-metalllb-on-kind.sh

.PHONY: deploy-kgateway
deploy-kgateway: package-kgateway-charts deploy-kgateway-crd-chart deploy-kgateway-chart ## Deploy the kgateway chart and CRDs

.PHONY: setup-base
setup-base: kind-create gw-api-crds gie-crds metallb ## Setup the base infrastructure (kind cluster, CRDs, and MetalLB)

.PHONY: setup
setup: setup-base kind-build-and-load package-kgateway-charts ## Setup the complete infrastructure (base setup plus images and charts)

.PHONY: run
run: setup deploy-kgateway  ## Set up complete development environment

.PHONY: undeploy
undeploy: undeploy-kgateway undeploy-kgateway-crds ## Undeploy the application from the cluster

.PHONY: undeploy-kgateway
undeploy-kgateway: ## Undeploy the core chart from the cluster
	$(HELM) uninstall kgateway --namespace $(INSTALL_NAMESPACE) || true

.PHONY: undeploy-kgateway-crds
undeploy-kgateway-crds: ## Undeploy the CRD chart from the cluster
	$(HELM) uninstall kgateway-crds --namespace $(INSTALL_NAMESPACE) || true

#----------------------------------------------------------------------------------
# Build assets for kubernetes e2e tests
#----------------------------------------------------------------------------------

kind-setup: ## Set up the KinD cluster. Deprecated: use kind-create instead.
	VERSION=${VERSION} CLUSTER_NAME=${CLUSTER_NAME} ./hack/kind/setup-kind.sh

kind-load-%:
	$(KIND) load docker-image $(IMAGE_REGISTRY)/$*:$(VERSION) --name $(CLUSTER_NAME)

# Build an image and load it into the KinD cluster
# Depends on: IMAGE_REGISTRY, VERSION, CLUSTER_NAME
# Envoy image may be specified via ENVOY_IMAGE on the command line or at the top of this file
kind-build-and-load-%: %-docker kind-load-% ; ## Use to build specified image and load it into kind

# Update the docker image used by a deployment
# This works for most of our deployments because the deployment name and container name both match
# NOTE TO DEVS:
#	I explored using a special format of the wildcard to pass deployment:image,
# 	but ran into some challenges with that pattern, while calling this target from another one.
#	It could be a cool extension to support, but didn't feel pressing so I stopped
kind-set-image-%:
	kubectl rollout pause deployment $* -n $(INSTALL_NAMESPACE) || true
	kubectl set image deployment/$* $*=$(IMAGE_REGISTRY)/$*:$(VERSION) -n $(INSTALL_NAMESPACE)
	kubectl patch deployment $* -n $(INSTALL_NAMESPACE) -p '{"spec": {"template":{"metadata":{"annotations":{"kgateway-kind-last-update":"$(shell date)"}}}} }'
	kubectl rollout resume deployment $* -n $(INSTALL_NAMESPACE)

# Reload an image in KinD
# This is useful to developers when changing a single component
# You can reload an image, which means it will be rebuilt and reloaded into the kind cluster, and the deployment
# will be updated to reference it
# Depends on: IMAGE_REGISTRY, VERSION, INSTALL_NAMESPACE , CLUSTER_NAME
# Envoy image may be specified via ENVOY_IMAGE on the command line or at the top of this file
kind-reload-%: kind-build-and-load-% kind-set-image-% ; ## Use to build specified image, load it into kind, and restart its deployment

.PHONY: kind-build-and-load ## Use to build all images and load them into kind
kind-build-and-load: kind-build-and-load-kgateway
kind-build-and-load: kind-build-and-load-envoy-wrapper
kind-build-and-load: kind-build-and-load-sds

.PHONY: kind-load ## Use to load all images into kind
kind-load: kind-load-kgateway
kind-load: kind-load-envoy-wrapper
kind-load: kind-load-sds

#----------------------------------------------------------------------------------
# Load Testing
#----------------------------------------------------------------------------------

.PHONY: run-load-tests
run-load-tests: ## Run KGateway load testing suite (requires existing cluster and installation)
	SKIP_INSTALL=true CLUSTER_NAME=$(CLUSTER_NAME) INSTALL_NAMESPACE=$(INSTALL_NAMESPACE) \
	go test -tags=e2e -v ./test/kubernetes/e2e/tests -run "^TestKgateway$$/^AttachedRoutes$$"

.PHONY: run-load-tests-baseline
run-load-tests-baseline: ## Run baseline load tests (1000 routes)
	SKIP_INSTALL=true CLUSTER_NAME=$(CLUSTER_NAME) INSTALL_NAMESPACE=$(INSTALL_NAMESPACE) \
	go test -tags=e2e -v ./test/kubernetes/e2e/tests -run "^TestKgateway$$/^AttachedRoutes$$/^TestAttachedRoutesBaseline$$"

.PHONY: run-load-tests-production
run-load-tests-production: ## Run production load tests (5000 routes)
	SKIP_INSTALL=true CLUSTER_NAME=$(CLUSTER_NAME) INSTALL_NAMESPACE=$(INSTALL_NAMESPACE) \
	go test -tags=e2e -v ./test/kubernetes/e2e/tests -run "^TestKgateway$$/^AttachedRoutes$$/^TestAttachedRoutesProduction$$"

#----------------------------------------------------------------------------------
# Targets for running Kubernetes Gateway API conformance tests
#----------------------------------------------------------------------------------

# Pull the conformance test suite from the k8s gateway api repo and copy it into the test dir.
$(TEST_ASSET_DIR)/conformance/conformance_test.go:
	mkdir -p $(TEST_ASSET_DIR)/conformance
	echo "//go:build conformance" > $@
	cat $(shell go list -json -m sigs.k8s.io/gateway-api | jq -r '.Dir')/conformance/conformance_test.go >> $@
	go fmt $@

CONFORMANCE_SUPPORTED_FEATURES ?= -supported-features=GatewayAddressEmpty,HTTPRouteParentRefPort,HTTPRouteRequestMirror,HTTPRouteBackendRequestHeaderModification,HTTPRouteNamedRouteRule,HTTPRouteDestinationPortMatching,HTTPRouteBackendProtocolH2C,HTTPRouteBackendProtocolWebSocket,HTTPRouteBackendTimeout,HTTPRouteHostRewrite,HTTPRouteMethodMatching,HTTPRoutePathRedirect,HTTPRoutePathRewrite,HTTPRoutePortRedirect,HTTPRouteQueryParamMatching,HTTPRouteRequestTimeout,HTTPRouteResponseHeaderModification,HTTPRouteSchemeRedirect,HTTPRouteCORS
CONFORMANCE_UNSUPPORTED_FEATURES ?= -exempt-features=GatewayPort8080,GatewayStaticAddresses,GatewayHTTPListenerIsolation,GatewayInfrastructurePropagation,HTTPRouteRequestMultipleMirrors,HTTPRouteRequestPercentageMirror
CONFORMANCE_SUPPORTED_PROFILES ?= -conformance-profiles=GATEWAY-HTTP,GATEWAY-TLS,GATEWAY-GRPC
CONFORMANCE_GATEWAY_CLASS ?= kgateway
CONFORMANCE_REPORT_ARGS ?= -report-output=$(TEST_ASSET_DIR)/conformance/$(VERSION)-report.yaml -organization=kgateway-dev -project=kgateway -version=$(VERSION) -url=github.com/kgateway-dev/kgateway -contact=github.com/kgateway-dev/kgateway/issues/new/choose
CONFORMANCE_ARGS := -gateway-class=$(CONFORMANCE_GATEWAY_CLASS) $(CONFORMANCE_SUPPORTED_FEATURES) $(CONFORMANCE_UNSUPPORTED_FEATURES) $(CONFORMANCE_SUPPORTED_PROFILES) $(CONFORMANCE_REPORT_ARGS)

# TODO [danehans]: Remove `kubectl wait` when gateway-api-inference-extension/issues/1315 is fixed.
.PHONY: conformance ## Run the conformance test suite
conformance: $(TEST_ASSET_DIR)/conformance/conformance_test.go ## Run the Gateway API conformance suite
	go test -mod=mod -ldflags='$(LDFLAGS)' -tags conformance -test.v $(TEST_ASSET_DIR)/conformance/... -args $(CONFORMANCE_ARGS)
	@echo "Waiting for gateway-conformance-infra namespace to terminate..."
	kubectl wait ns gateway-conformance-infra --for=delete --timeout=2m || true

# Run only the specified conformance test. The name must correspond to the ShortName of one of the k8s gateway api
# conformance tests.
# TODO [danehans]: Remove `kubectl wait` when gateway-api-inference-extension/issues/1315 is fixed.
conformance-%: $(TEST_ASSET_DIR)/conformance/conformance_test.go ## Run only the specified Gateway API conformance test by ShortName
	go test -mod=mod -ldflags='$(LDFLAGS)' -tags conformance -test.v $(TEST_ASSET_DIR)/conformance/... -args $(CONFORMANCE_ARGS) \
	-run-test=$*
	@echo "Waiting for gateway-conformance-infra namespace to terminate..."
	kubectl wait ns gateway-conformance-infra --for=delete --timeout=2m || true

#----------------------------------------------------------------------------------
# Targets for running Gateway API Inference Extension conformance tests
#----------------------------------------------------------------------------------

# Reporting flags, identical to CONFORMANCE_REPORT_ARGS but with "inference-"
GIE_CONFORMANCE_REPORT_ARGS ?= \
    -report-output=$(TEST_ASSET_DIR)/conformance/inference-$(VERSION)-report.yaml \
    -organization=kgateway-dev \
    -project=kgateway \
    -version=$(VERSION) \
    -url=github.com/kgateway-dev/kgateway \
    -contact=github.com/kgateway-dev/kgateway/issues/new/choose

# The args to pass into the Gateway API Inference Extension conformance test suite.
GIE_CONFORMANCE_ARGS := \
    -gateway-class=$(CONFORMANCE_GATEWAY_CLASS) \
    $(GIE_CONFORMANCE_REPORT_ARGS)

INFERENCE_CONFORMANCE_DIR := $(shell go list -m -f '{{.Dir}}' sigs.k8s.io/gateway-api-inference-extension)/conformance

# TODO [danehans]: Remove `kubectl wait` when gateway-api-inference-extension/issues/1315 is fixed.
.PHONY: gie-conformance
gie-conformance: gie-crds ## Run the Gateway API Inference Extension conformance suite
	@mkdir -p $(TEST_ASSET_DIR)/conformance
	go test -mod=mod -ldflags='$(LDFLAGS)' \
	    -tags conformance \
	    -timeout=25m \
	    -v $(INFERENCE_CONFORMANCE_DIR) \
	    -args $(GIE_CONFORMANCE_ARGS)
	@echo "Waiting for gateway-conformance-infra namespace to terminate..."
	kubectl wait ns gateway-conformance-infra --for=delete --timeout=2m || true

# TODO [danehans]: Remove `kubectl wait` when gateway-api-inference-extension/issues/1315 is fixed.
.PHONY: gie-conformance-%
gie-conformance-%: gie-crds ## Run only the specified Gateway API Inference Extension conformance test by ShortName
	@mkdir -p $(TEST_ASSET_DIR)/conformance
	go test -mod=mod -ldflags='$(LDFLAGS)' \
	    -tags conformance \
	    -timeout=25m \
	    -v $(INFERENCE_CONFORMANCE_DIR) \
	    -args $(GIE_CONFORMANCE_ARGS) -run-test=$*
	@echo "Waiting for gateway-conformance-infra namespace to terminate..."
	kubectl wait ns gateway-conformance-infra --for=delete --timeout=2m || true

# An alias to run both Gateway API and Inference Extension conformance tests.
.PHONY: all-conformance
all-conformance: conformance gie-conformance agw-conformance ## Run all conformance test suites
	@echo "All conformance suites have completed."

#----------------------------------------------------------------------------------
# Targets for running Agent Gateway conformance tests
#----------------------------------------------------------------------------------

# Agent Gateway conformance test configuration
AGW_CONFORMANCE_SUPPORTED_FEATURES ?= -supported-features=GatewayInfrastructurePropagation,HTTPRouteBackendProtocolH2C,HTTPRouteBackendProtocolWebSocket,HTTPRouteHostRewrite,HTTPRouteMethodMatching,HTTPRoutePathRedirect,HTTPRoutePathRewrite,HTTPRoutePortRedirect,HTTPRouteQueryParamMatching,HTTPRouteResponseHeaderModification,HTTPRouteSchemeRedirect,HTTPRouteCORS
AGW_CONFORMANCE_UNSUPPORTED_FEATURES ?= -exempt-features=GatewayPort8080,GatewayStaticAddresses,GatewayHTTPListenerIsolation,HTTPRouteRequestMultipleMirrors,HTTPRouteRequestPercentageMirror
AGW_CONFORMANCE_SUPPORTED_PROFILES ?= -conformance-profiles=GATEWAY-HTTP
AGW_CONFORMANCE_GATEWAY_CLASS ?= agentgateway
AGW_CONFORMANCE_REPORT_ARGS ?= -report-output=$(TEST_ASSET_DIR)/conformance/agw-$(VERSION)-report.yaml -organization=kgateway-dev -project=kgateway -version=$(VERSION) -url=github.com/kgateway-dev/kgateway -contact=github.com/kgateway-dev/kgateway/issues/new/choose
AGW_CONFORMANCE_ARGS := -gateway-class=$(AGW_CONFORMANCE_GATEWAY_CLASS) $(AGW_CONFORMANCE_SUPPORTED_FEATURES) $(AGW_CONFORMANCE_UNSUPPORTED_FEATURES) $(AGW_CONFORMANCE_SUPPORTED_PROFILES) $(AGW_CONFORMANCE_REPORT_ARGS)

# TODO [danehans]: Remove `kubectl wait` when gateway-api-inference-extension/issues/1315 is fixed.
.PHONY: agw-conformance ## Run the agent gateway conformance test suite
agw-conformance: $(TEST_ASSET_DIR)/conformance/conformance_test.go
	CONFORMANCE_GATEWAY_CLASS=$(AGW_CONFORMANCE_GATEWAY_CLASS) go test -mod=mod -ldflags='$(LDFLAGS)' -tags conformance -test.v $(TEST_ASSET_DIR)/conformance/... -args $(AGW_CONFORMANCE_ARGS)
	@echo "Waiting for gateway-conformance-infra namespace to terminate..."
	kubectl wait ns gateway-conformance-infra --for=delete --timeout=2m || true

# TODO [danehans]: Remove `kubectl wait` when gateway-api-inference-extension/issues/1315 is fixed.
# Run only the specified agent gateway conformance test
agw-conformance-%: $(TEST_ASSET_DIR)/conformance/conformance_test.go
	CONFORMANCE_GATEWAY_CLASS=$(AGW_CONFORMANCE_GATEWAY_CLASS) go test -mod=mod -ldflags='$(LDFLAGS)' -tags conformance -test.v $(TEST_ASSET_DIR)/conformance/... -args $(AGW_CONFORMANCE_ARGS) \
	-run-test=$*
	@echo "Waiting for gateway-conformance-infra namespace to terminate..."
	kubectl wait ns gateway-conformance-infra --for=delete --timeout=2m || true

#----------------------------------------------------------------------------------
# Dependency Bumping
#----------------------------------------------------------------------------------

.PHONY: bump-gtw
bump-gtw: ## Bump Gateway API deps to $DEP_REF (or $DEP_VERSION). Example: make bump-gtw DEP_REF=198e6cab...
	@if [ -z "$${DEP_REF:-}" ] && [ -n "$${DEP_VERSION:-}" ]; then DEP_REF="$$DEP_VERSION"; fi; \
	if [ -z "$${DEP_REF:-}" ]; then \
	  echo "DEP_REF is not set (or DEP_VERSION). e.g. make bump-gtw DEP_REF=v1.3.0 or DEP_REF=198e6cab6774..."; \
	  exit 2; \
	fi; \
	echo "Bumping Gateway API to $${DEP_REF}"; \
	hack/bump_deps.sh gtw "$$DEP_REF"; \
	echo "Updating licensing..."; \
	$(MAKE) generate-licenses

.PHONY: bump-gie
bump-gie: ## Bump Gateway API Inference Extension to $DEP_REF (or $DEP_VERSION). Example: make bump-gie DEP_REF=198e6cab...
	@if [ -z "$${DEP_REF:-}" ] && [ -n "$${DEP_VERSION:-}" ]; then DEP_REF="$$DEP_VERSION"; fi; \
	if [ -z "$${DEP_REF:-}" ]; then \
	  echo "DEP_REF is not set (or DEP_VERSION). e.g. make bump-gie DEP_REF=v0.5.1 or DEP_REF=198e6cab6774..."; \
	  exit 2; \
	fi; \
	echo ">>> Bumping Gateway API Inference Extension to $${DEP_REF}"; \
	hack/bump_deps.sh gie "$$DEP_REF"; \
	echo "Updating licensing..."; \
	$(MAKE) generate-licenses

#----------------------------------------------------------------------------
# Info
#----------------------------------------------------------------------------

.PHONY: envoyversion
envoyversion: ENVOY_VERSION_TAG ?= $(shell echo $(ENVOY_IMAGE) | cut -d':' -f2)
envoyversion:
	echo "Version is $(ENVOY_VERSION_TAG)"
	echo "Commit for envoyproxy is $(shell curl -s https://raw.githubusercontent.com/solo-io/envoy-gloo/refs/tags/v$(ENVOY_VERSION_TAG)/bazel/repository_locations.bzl | grep "envoy =" -A 4 | grep commit | cut -d'"' -f2)"
	echo "Current ABI in envoyinit can be found in the cargo.toml's envoy-proxy-dynamic-modules-rust-sdk"

#----------------------------------------------------------------------------------
# Printing makefile variables utility
#----------------------------------------------------------------------------------

# use `make print-MAKEFILE_VAR` to print the value of MAKEFILE_VAR

print-%  : ; @echo $($*)
