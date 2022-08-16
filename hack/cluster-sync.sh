#!/usr/bin/env bash

set -euo pipefail

# ******************************************************
# Create service account and kubeconfig to access tenant cluster
# ******************************************************
./kubevirtci kubectl -n kvcluster create -f ./deploy/infra-cluster-service-account.yaml

export INFRA_KUBECONFIG_IN_TENANT_FILE=_ci-configs/infra_kubeconfig.yaml
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

# ******************************************************
# Add kubeconfig to secret and create it in tenant
export INFRA_KUBECONFIG_IN_TENANT_CONTENT=$(cat $INFRA_KUBECONFIG_IN_TENANT_FILE | base64 -w 0)
envsubst < ./deploy/secret-template.yaml > _ci-configs/tenant_secret.yaml

./kubevirtci kubectl-tenant create -f ./deploy/000-namespace.yaml
./kubevirtci kubectl-tenant create -f _ci-configs/tenant_secret.yaml

# ******************************************************
# Apply config
./kubevirtci kubectl-tenant create -f ./deploy/configmap-template.yaml

# ******************************************************
# Finally deploy the driver
# ******************************************************
# TODO:  The yaml should reference the container image in the local kubevirtci repo (from the image push step)

./kubevirtci kubectl-tenant create -f ./deploy/000-csi-driver.yaml
./kubevirtci kubectl-tenant create -f ./deploy/020-autorization.yaml
./kubevirtci kubectl-tenant create -f ./deploy/030-node.yaml
./kubevirtci kubectl-tenant create -f ./deploy/040-controller.yaml

# ******************************************************
# Edit storage class
#- infraStorageClassName: standard
#+ infraStorageClassName: rook-ceph-block

export INFRA_KUBECONFIG_STORAGECLASS_FILE=_ci-configs/storageclass.yaml
cp ./deploy/example/storageclass.yaml $INFRA_KUBECONFIG_STORAGECLASS_FILE
sed -i -r 's/standard/rook-ceph-block/g' $INFRA_KUBECONFIG_STORAGECLASS_FILE
./kubevirtci kubectl-tenant create -f $INFRA_KUBECONFIG_STORAGECLASS_FILE

