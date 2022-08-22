#!/usr/bin/env bash

set -euo pipefail

RESOURCES_DIR=_ci-configs
mkdir -p ${RESOURCES_DIR}
INFRA_KUBECONFIG_IN_TENANT_FILE=${RESOURCES_DIR}/infra_kubeconfig.yaml
TENANT_SECRET_FILE=${RESOURCES_DIR}/tenant_secret.yaml
INFRA_KUBECONFIG_STORAGECLASS_FILE=${RESOURCES_DIR}/storageclass.yaml

# Create kubeconfig to access infra cluster from tenant cluster
function cluster::create_infra_kubeconfig() {
    export INFRA_KUBECONFIG_IN_TENANT_FILE
  ./hack/create-infra-kubeconfig.sh  > $INFRA_KUBECONFIG_IN_TENANT_FILE
  #  Try to find the url in universal way: maybe it is node ip and service port??
  #         sed -i -r 's/127.0.0.1:[0-9]+/192.168.66.101:6443/g' kubeconfig-e2e
  #
  #❯ ./kubevirtci kubectl get node -o wide
  #NAME     STATUS   ROLES                         AGE   VERSION   INTERNAL-IP      EXTERNAL-IP   OS-IMAGE          KERNEL-VERSION          CONTAINER-RUNTIME
  #node01   Ready    control-plane,master,worker   46m   v1.23.6   192.168.66.101   <none>        CentOS Stream 8   4.18.0-383.el8.x86_64   cri-o://1.22.4
  #
  #❯ ./kubevirtci kubectl get svc -o wide -n kvcluster
  #NAME           TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)    AGE   SELECTOR
  #kvcluster-lb   ClusterIP   10.101.163.253   <none>        6443/TCP   41m   cluster.x-k8s.io/cluster-name=kvcluster,cluster.x-k8s.io/role=control-plane
  #
  sed -i -r 's/127.0.0.1:[0-9]+/192.168.66.101:6443/g' $INFRA_KUBECONFIG_IN_TENANT_FILE
}

# Add kubeconfig as base64 to secret
function cluster::add_kubeconfig_to_secret() {
  export INFRA_KUBECONFIG_IN_TENANT_CONTENT=$(cat $INFRA_KUBECONFIG_IN_TENANT_FILE | base64 -w 0)
  envsubst < ./deploy/secret-template.yaml > ${TENANT_SECRET_FILE}
}

function cluster::install_csi_driver() {
#  export MANIFEST_IMG="registry:5000/kubevirt-csi-driver"
# 192.168.66.2 is the ip of registry visible from tenant cluster, from the VMI
  export MANIFEST_IMG="192.168.66.2:5000/kubevirt-csi-driver"
  export MANIFEST_TAG="latest"

  # TODO: ugly, needs to be changed,
  # also the image reference can be made configurable, if external registry is used like quay.io it can be used here
  sed -r "s#quay.io/kubevirt/csi-driver:latest#${MANIFEST_IMG}:${MANIFEST_TAG}#g" ./deploy/030-node.yaml > ${RESOURCES_DIR}/030-node.yaml
  sed -r "s#quay.io/kubevirt/csi-driver:latest#${MANIFEST_IMG}:${MANIFEST_TAG}#g" ./deploy/040-controller.yaml > ${RESOURCES_DIR}/040-controller.yaml

  ./kubevirtci kubectl-tenant apply -f ./deploy/000-csi-driver.yaml
  ./kubevirtci kubectl-tenant apply -f ./deploy/020-autorization.yaml
  ./kubevirtci kubectl-tenant apply -f ${RESOURCES_DIR}/030-node.yaml
  ./kubevirtci kubectl-tenant apply -f ${RESOURCES_DIR}/040-controller.yaml
}

# Apply storage class used by csi.kubevirt.io
function cluster::create_storageclass() {
  export INFRA_KUBECONFIG_STORAGECLASS_FILE
  cp ./deploy/example/storageclass.yaml $INFRA_KUBECONFIG_STORAGECLASS_FILE
  sed -i -r 's/standard/local/g' $INFRA_KUBECONFIG_STORAGECLASS_FILE
  ./kubevirtci kubectl-tenant apply -f $INFRA_KUBECONFIG_STORAGECLASS_FILE
}

# ******************************************************
# Prepare cluster for csi driver
# ******************************************************
./kubevirtci kubectl -n kvcluster apply -f ./deploy/infra-cluster-service-account.yaml
./kubevirtci kubectl-tenant apply -f ./deploy/000-namespace.yaml
cluster::create_infra_kubeconfig
cluster::add_kubeconfig_to_secret
./kubevirtci kubectl-tenant apply -f ${TENANT_SECRET_FILE}
./kubevirtci kubectl-tenant apply -f ./deploy/configmap-template.yaml

# ******************************************************
# Build the driver
# ******************************************************
./kubevirtci build

# ******************************************************
# Deploy the driver
# ******************************************************
cluster::install_csi_driver
cluster::create_storageclass

# ******************************************************
# Wait for driver to rollout
# ******************************************************
./kubevirtci kubectl-tenant rollout status ds/kubevirt-csi-node -n kubevirt-csi-driver --timeout=5m
./kubevirtci kubectl-tenant rollout status deployment/kubevirt-csi-controller -n kubevirt-csi-driver --timeout=5m
