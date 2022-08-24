#!/usr/bin/env bash

set -euo pipefail


RESOURCES_DIR="./deploy/split-infra-tenant"
TMP_RESOURCES_DIR=_ci-configs
mkdir -p ${TMP_RESOURCES_DIR}
TENANT_SECRET_FILE=${TMP_RESOURCES_DIR}/tenant_secret.yaml
INFRA_KUBECONFIG_STORAGECLASS_FILE=${TMP_RESOURCES_DIR}/storageclass.yaml

function cluster::install_csi_driver_ds() {
	export MANIFEST_IMG="192.168.66.2:5000/kubevirt-csi-driver"
	export MANIFEST_TAG="latest"

	sed -r "s#quay.io/kubevirt/csi-driver:latest#${MANIFEST_IMG}:${MANIFEST_TAG}#g" ${RESOURCES_DIR}/030-node.yaml > ${TMP_RESOURCES_DIR}/030-node.yaml

	./kubevirtci kubectl-tenant apply -f ${RESOURCES_DIR}/000-namespace.yaml
	./kubevirtci kubectl-tenant apply -f ${RESOURCES_DIR}/000-csi-driver.yaml
	./kubevirtci kubectl-tenant apply -f ${RESOURCES_DIR}/020-autorization.yaml
	./kubevirtci kubectl-tenant apply -f ${TMP_RESOURCES_DIR}/030-node.yaml
}

function cluster::install_csi_driver_controller() {
	export MANIFEST_IMG="registry:5000/kubevirt-csi-driver"
	export MANIFEST_TAG="latest"

	sed -r "s#quay.io/kubevirt/csi-driver:latest#${MANIFEST_IMG}:${MANIFEST_TAG}#g" ${RESOURCES_DIR}/040-controller.yaml > ${TMP_RESOURCES_DIR}/040-controller.yaml

	./kubevirtci kubectl -n kvcluster apply -f ${RESOURCES_DIR}/infra-cluster-service-account.yaml
	./kubevirtci kubectl -n kvcluster apply -f ${RESOURCES_DIR}/configmap-template.yaml

	# TODO Eventually find way to use a non-admin tenant node kubeconfig
	./kubevirtci kubectl -n kvcluster apply -f ${TMP_RESOURCES_DIR}/040-controller.yaml
}

# Apply storage class used by csi.kubevirt.io
function cluster::create_storageclass() {
	export INFRA_KUBECONFIG_STORAGECLASS_FILE
	cp ./deploy/example/storageclass.yaml $INFRA_KUBECONFIG_STORAGECLASS_FILE
	sed -i -r 's/standard/local/g' $INFRA_KUBECONFIG_STORAGECLASS_FILE
	./kubevirtci kubectl-tenant apply -f $INFRA_KUBECONFIG_STORAGECLASS_FILE
}

# ******************************************************
# Build the driver
# ******************************************************
./kubevirtci build

# ******************************************************
# Deploy the driver
# ******************************************************

# Deploys the DS into the tenant cluster
cluster::install_csi_driver_ds

# Deploys the controller into the infra cluster
cluster::install_csi_driver_controller

cluster::create_storageclass

# ******************************************************
# Wait for driver to rollout
# ******************************************************

./kubevirtci kubectl-tenant rollout status ds/kubevirt-csi-node -n kubevirt-csi-driver --timeout=5m
./kubevirtci kubectl rollout status deployment/kubevirt-csi-controller -n kvcluster --timeout=5m
