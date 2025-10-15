#!/bin/bash -ex

# 0. Assign default values to some of our environment variables
# Get directory this script is located in to access script local files
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# The name of the kind cluster to deploy to
CLUSTER_NAME="${CLUSTER_NAME:-kind}"
# The version of the Node Docker image to use for booting the cluster
CLUSTER_NODE_VERSION="${CLUSTER_NODE_VERSION:-v1.33.1@sha256:050072256b9a903bd914c0b2866828150cb229cea0efe5892e2b644d5dd3b34f}"
# The version used to tag images
VERSION="${VERSION:-1.0.0-ci1}"
# Skip building docker images if we are testing a released version
SKIP_DOCKER="${SKIP_DOCKER:-false}"
# Stop after creating the kind cluster
JUST_KIND="${JUST_KIND:-false}"
# If true, run extra steps to set up k8s gateway api conformance test environment
CONFORMANCE="${CONFORMANCE:-false}"
# The version of the k8s gateway api conformance tests to run. Requires CONFORMANCE=true
CONFORMANCE_VERSION="${CONFORMANCE_VERSION:-$(go list -m sigs.k8s.io/gateway-api | awk '{print $2}')}"
# The channel of the k8s gateway api conformance tests to run. Requires CONFORMANCE=true
CONFORMANCE_CHANNEL="${CONFORMANCE_CHANNEL:-"experimental"}"
# The version of the k8s gateway api inference extension CRDs to install. Requires CONFORMANCE=true
# Managed by `make bump-gie`.
GIE_CRD_VERSION="51485db93d63bfa2f9264460798671b72bdf9f5d"
# The kind CLI to use. Defaults to the latest version from the kind repo.
KIND="${KIND:-go tool kind}"
# The helm CLI to use. Defaults to the latest version from the helm repo.
HELM="${HELM:-go tool helm}"
# If true, use localstack for lambda functions
LOCALSTACK="${LOCALSTACK:-false}"

# Export the variables so they are available in the environment
export VERSION CLUSTER_NAME

function create_kind_cluster_or_skip() {
  activeClusters=$(kind get clusters)

  # if the kind cluster exists already, return
  if [[ "$activeClusters" =~ .*"$CLUSTER_NAME".* ]]; then
    echo "cluster exists, skipping cluster creation"
    return
  fi

  echo "creating cluster ${CLUSTER_NAME}"
  $KIND create cluster \
    --name "$CLUSTER_NAME" \
    --image "kindest/node:$CLUSTER_NODE_VERSION" \
    --config="$SCRIPT_DIR/cluster.yaml"
  echo "Finished setting up cluster $CLUSTER_NAME"

  # so that you can just build the kind image alone if needed
  if [[ $JUST_KIND == 'true' ]]; then
    echo "JUST_KIND=true, not building images"
    exit
  fi
}

# 1. Create a kind cluster (or skip creation if a cluster with name=CLUSTER_NAME already exists)
# This config is roughly based on: https://kind.sigs.k8s.io/docs/user/ingress/
create_kind_cluster_or_skip

if [[ $SKIP_DOCKER == 'true' ]]; then
  # TODO(tim): refactor the Makefile & CI scripts so we're loading local
  # charts to real helm repos, and then we can remove this block.
  echo "SKIP_DOCKER=true, not building images or chart"
else
  # 2. Make all the docker images and load them to the kind cluster
  VERSION=$VERSION CLUSTER_NAME=$CLUSTER_NAME make kind-build-and-load

  # 3. Build the test helm chart, ensuring we have a chart in the `_test` folder
  VERSION=$VERSION make package-kgateway-charts

  # 4. Build the mock ai provider docker image and load it to the kind cluster when the ai extension setup is enabled
  if [[ $CONFORMANCE == "true" ]]; then
    VERSION=$VERSION make kind-build-and-load-test-ai-provider
  fi

fi

# 5. Apply the Kubernetes Gateway API CRDs
# Note, we're using kustomize to apply the CRDs from the k8s gateway api repo as
# kustomize supports remote GH URLs and provides more flexibility compared to
# alternatives like running a series of `kubectl apply -f <url>` commands. This
# approach is largely necessary since upstream hasn't adopted a helm chart for
# the CRDs yet, or won't be for the foreseeable future.
if [[ $CONFORMANCE_CHANNEL == "standard" ]]; then
  kubectl apply --server-side --kustomize "https://github.com/kubernetes-sigs/gateway-api/config/crd?ref=$CONFORMANCE_VERSION"
else
  kubectl apply --server-side --kustomize "https://github.com/kubernetes-sigs/gateway-api/config/crd/$CONFORMANCE_CHANNEL?ref=$CONFORMANCE_VERSION"
fi

# 6. Apply the Kubernetes Gateway API Inference Extension CRDs
kubectl apply --kustomize "https://github.com/kubernetes-sigs/gateway-api-inference-extension/config/crd?ref=$GIE_CRD_VERSION"

# 7. Conformance test setup
if [[ $CONFORMANCE == "true" ]]; then
  echo "Running conformance test setup"
  . $SCRIPT_DIR/setup-metalllb-on-kind.sh
fi

# 9. Setup localstack
if [[ $LOCALSTACK == "true" ]]; then
  echo "Setting up localstack"
  . $SCRIPT_DIR/setup-localstack.sh
fi
