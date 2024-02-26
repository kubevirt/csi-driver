#!/usr/bin/env bash
source hack/common.sh
set -euo pipefail

TENANT_CLUSTER_NAMESPACE=${TENANT_CLUSTER_NAMESPACE:-kvcluster}
CSI_DRIVER_NAMESPACE=${CSI_DRIVER_NAMESPACE:-kubevirt-csi-driver}
INFRA_STORAGE_CLASS=${INFRA_STORAGE_CLASS:-rook-ceph-block}

INFRA_REGISTRY=${REGISTRY:-registry:5000}
REGISTRY=${REGISTRY:-192.168.66.2:5000}
TARGET_NAME=${TARGET_NAME:-kubevirt-csi-driver}
TAG=${TAG:-latest}

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

function cluster::generate_infra_controller_overlay() {
  cat <<- END > ./deploy/controller-infra/dev-overlay/controller.yaml
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
          image: $INFRA_REGISTRY/$TARGET_NAME:$TAG
END
}

function cluster::generate_controller_dev_kustomization() {
  cat <<- END > ./deploy/$1/dev-overlay/kustomization.yaml
resources:
- ../base
- infra-namespace-configmap.yaml
namespace: $2
patches:
- path: controller.yaml
END
}

# ******************************************************
# Build the driver
# ******************************************************
./kubevirtci build

# ******************************************************
# Create namespace and put infra cluster secret in it
# ******************************************************
mkdir -p ./deploy/controller-infra/dev-overlay
mkdir -p ./deploy/tenant/dev-overlay

cluster::generate_controller_rbac $TENANT_CLUSTER_NAMESPACE
cluster::generate_tenant_dev_kustomization
cluster::generate_controller_dev_kustomization "controller-infra" $TENANT_CLUSTER_NAMESPACE
tenant::deploy_csidriver_namespace $CSI_DRIVER_NAMESPACE
_kubectl -n $TENANT_CLUSTER_NAMESPACE apply -f ./deploy/infra-cluster-service-account.yaml

# ******************************************************
# Generate kustomize overlay for development environment
# ******************************************************
cluster::generate_driver_configmap_overlay "tenant"
cluster::generate_driver_configmap_overlay "controller-infra"
cluster::generate_infra_controller_overlay
cluster::generate_node_overlay
cluster::generate_storageclass_overlay "tenant" $INFRA_STORAGE_CLASS
cluster::patch_local_storage_profile

# ******************************************************
# Deploy the snapshot resources
# ******************************************************
tenant::deploy_snapshotresources

# ******************************************************
# Deploy the tenant yaml
# ******************************************************
_kubectl_tenant apply --kustomize ./deploy/tenant/dev-overlay
# ******************************************************
# Deploy the controller yaml
# ******************************************************
_kubectl apply --kustomize ./deploy/controller-infra/dev-overlay


# ******************************************************
# Wait for driver to rollout
# ******************************************************
_kubectl_tenant rollout status ds/kubevirt-csi-node -n $CSI_DRIVER_NAMESPACE --timeout=10m
_kubectl rollout status deployment/kubevirt-csi-controller -n $TENANT_CLUSTER_NAMESPACE --timeout=10m
