#!/bin/bash
SERVICE_ACCOUNT_NAME=$1

TOKEN_NAME=$(kubectl get serviceaccount kubevirt-csi -o json | jq '.secrets[0].name' | xargs echo)
export CA_CRT=$(kubectl get secret $TOKEN_NAME -o json | jq '.data["ca.crt"]' | xargs echo)
export TOKEN=$(kubectl get secret $TOKEN_NAME -o json | jq '.data["token"]' | xargs echo | base64 -d)

# KUBECONFIG_PATH=${KUBECONFIG:=~/.kube/config}
KUBECONFIG_CONTENT=$(kubectl config view -o json)
CURRENT_CONTEXT=$(echo $KUBECONFIG_CONTENT | jq '.["current-context"]' | xargs echo)
CLUSTER_SPEC_NAME=$(echo $KUBECONFIG_CONTENT | jq -c '.contexts' | jq -c '.[] | select( .name == env.CURRENT_CONTEXT )' | jq '.context.cluster' | xargs echo)
export SERVER_URL=$(echo $KUBECONFIG_CONTENT | jq -c '.clusters' | jq -c '.[] | select( .name == env.CLUSTER_SPEC_NAME )' | jq '.cluster.server' | xargs echo)

envsubst < hack/kubeconfig_template.yaml