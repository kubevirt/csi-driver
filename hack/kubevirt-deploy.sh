#!/usr/bin/env bash
source hack/common.sh

export KUBECONFIG=$(cluster-up/kubeconfig.sh)

KUBEVIRT_VERSION=$(get_latest_release "kubevirt/kubevirt")
CDI_VERSION=$(get_latest_release "kubevirt/containerized-data-importer")

echo "KUBEVIRT_VERSION = ${KUBEVIRT_VERSION}, CDI_VERSION = ${CDI_VERSION}"

# Deploy Kubevirt
kubectl create -f "https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_VERSION}/kubevirt-operator.yaml"

kubectl create -f "https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_VERSION}/kubevirt-cr.yaml"

# TODO: change this to actually work and change hotplug options
kubectl apply -f - <<EOF
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubevirt-config
  namespace: kubevirt
data:
  feature-gates: "DataVolumes,LiveMigration,CPUManager,CPUNodeDiscovery,Sidecar,Snapshot,HotplugVolumes"
---
EOF

# Deploy Storage
kubectl create -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${CDI_VERSION}/cdi-operator.yaml"

kubectl create -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${CDI_VERSION}/cdi-cr.yaml"

# Wait for kubevirt to be available
kubectl -n kubevirt wait kv kubevirt --for condition=Available --timeout 10m
kubectl rollout status -n cdi deployment/cdi-operator --timeout 10m
