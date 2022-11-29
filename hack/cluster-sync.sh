#!/usr/bin/env bash
source hack/common.sh
set -euo pipefail

TENANT_CLUSTER_NAMESPACE=${TENANT_CLUSTER_NAMESPACE:-kvcluster}
CSI_DRIVER_NAMESPACE=${CSI_DRIVER_NAMESPACE:-kubevirt-csi-driver}
INFRA_STORAGE_CLASS=${INFRA_STORAGE_CLASS:-local}
REGISTRY=${REGISTRY:-192.168.66.2:5000}
TARGET_NAME=${TARGET_NAME:-kubevirt-csi-driver}
TAG=${TAG:-latest}

function tenant::deploy_kubeconfig_secret() {
  TOKEN_NAME=$(_kubectl -n $TENANT_CLUSTER_NAMESPACE get serviceaccount/kubevirt-csi -o jsonpath='{.secrets[0].name}')
  CA_CRT=$(_kubectl -n $TENANT_CLUSTER_NAMESPACE get secret $TOKEN_NAME -o json | jq '.data["ca.crt"]' | xargs echo)
  TOKEN=$(_kubectl -n $TENANT_CLUSTER_NAMESPACE get secret $TOKEN_NAME -o json | jq '.data["token"]' | xargs echo | base64 -d)
  INTERNAL_IP=$(_kubectl get node -l "node-role.kubernetes.io/control-plane" -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')

  kubeconfig=$(cat <<- END
apiVersion: v1
clusters:
- cluster:
    insecure-skip-tls-verify: true
    server: https://$INTERNAL_IP:6443
  name: infra-cluster
contexts:
- context:
    cluster: infra-cluster
    namespace: $TENANT_CLUSTER_NAMESPACE
    user: kubevirt-csi
  name: only-context
current-context: only-context
kind: Config
preferences: {}
users:
- name: kubevirt-csi
  user:
    token: $TOKEN
END
)

  cat <<- END | ./kubevirtci kubectl-tenant apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: infra-cluster-credentials
  namespace: $CSI_DRIVER_NAMESPACE
data:
  kubeconfig: $(echo "$kubeconfig" | base64 -w 0)
END
}

function cluster::generate_tenant_controller_overlay() {
  cat <<- END > ./deploy/controller-tenant/dev-overlay/controller.yaml
kind: Deployment
apiVersion: apps/v1
metadata:
  name: kubevirt-csi-controller
  namespace: kubevirt-csi-driver
  labels:
    app: kubevirt-csi-driver
spec:
  template:
    spec:
      containers:
        - name: csi-driver
          image: $REGISTRY/$TARGET_NAME:$TAG
END
}

function cluster::generate_node_overlay() {
  cat <<- END > ./deploy/tenant/dev-overlay/node.yaml
kind: DaemonSet
apiVersion: apps/v1
metadata:
  name: kubevirt-csi-node
  namespace: kubevirt-csi-driver
spec:
  template:
    spec:
      containers:
        - name: csi-driver
          image: $REGISTRY/$TARGET_NAME:$TAG
END
}

# ******************************************************
# Build the driver
# ******************************************************
./kubevirtci build

# ******************************************************
# Create namespace and put infra cluster secret in it
# ******************************************************
mkdir -p ./deploy/controller-tenant/dev-overlay
mkdir -p ./deploy/tenant/dev-overlay
cluster::generate_tenant_dev_kustomization
cluster::generate_controller_dev_kustomization "controller-tenant" $CSI_DRIVER_NAMESPACE
tenant::deploy_csidriver_namespace $CSI_DRIVER_NAMESPACE
_kubectl -n $TENANT_CLUSTER_NAMESPACE apply -f ./deploy/infra-cluster-service-account.yaml

# ******************************************************
# Generate kustomize overlay for development environment
# ******************************************************
tenant::deploy_kubeconfig_secret
cluster::generate_driver_configmap_overlay "tenant"
cluster::generate_tenant_controller_overlay
cluster::generate_node_overlay
cluster::generate_storageclass_overlay "tenant" $INFRA_STORAGE_CLASS

# ******************************************************
# Deploy the tenant yaml
# ******************************************************
_kubectl_tenant apply --kustomize ./deploy/tenant/dev-overlay
# ******************************************************
# Deploy the controller yaml
# ******************************************************
_kubectl_tenant apply --kustomize ./deploy/controller-tenant/dev-overlay

# ******************************************************
# Wait for driver to rollout
# ******************************************************
_kubectl_tenant rollout status ds/kubevirt-csi-node -n $CSI_DRIVER_NAMESPACE --timeout=5m
_kubectl_tenant rollout status deployment/kubevirt-csi-controller -n $CSI_DRIVER_NAMESPACE --timeout=5m
