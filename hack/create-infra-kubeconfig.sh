#!/usr/bin/env bash

set -euo pipefail

#SERVICE_ACCOUNT_NAME=kubevirt-csi
TOKEN_NAME=$(./kubevirtci kubectl -n kvcluster get serviceaccount/kubevirt-csi -o jsonpath='{.secrets[0].name}')
export CA_CRT=$(./kubevirtci kubectl -n kvcluster get secret $TOKEN_NAME -o json | jq '.data["ca.crt"]' | xargs echo)
export TOKEN=$(./kubevirtci kubectl -n kvcluster get secret $TOKEN_NAME -o json | jq '.data["token"]' | xargs echo | base64 -d)

KUBECONFIG_CONTENT=$(./kubevirtci kubectl config view -o json)
export CURRENT_CONTEXT=$(echo $KUBECONFIG_CONTENT | jq '.["current-context"]' | xargs echo)
export CLUSTER_SPEC_NAME=$(echo $KUBECONFIG_CONTENT | jq -c '.contexts' | jq -c '.[] | select( .name == env.CURRENT_CONTEXT )' | jq '.context.cluster' | xargs echo)
export SERVER_URL=$(echo $KUBECONFIG_CONTENT | jq -c '.clusters' | jq -c '.[] | select( .name == env.CLUSTER_SPEC_NAME )' | jq '.cluster.server' | xargs echo)

envsubst < ./deploy/infra-cluster-kubeconfig-template.yaml
