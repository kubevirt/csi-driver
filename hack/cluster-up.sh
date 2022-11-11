#!/usr/bin/env bash

set -euo pipefail
export TENANT_CLUSTER_NAMESPACE=${TENANT_CLUSTER_NAMESPACE:-kvcluster}

# ******************************************************
# Start infra cluster with tenant cluster
# ******************************************************
echo "Starting base cluster"
./kubevirtci up
echo "Installing capk"
./kubevirtci install-capk
echo "Creating $TENANT_CLUSTER_NAMESPACE"
./kubevirtci create-cluster

echo "Waiting for $TENANT_CLUSTER_NAMESPACE vmis to be ready"
./kubevirtci kubectl wait --for=condition=Ready vmi -l capk.cluster.x-k8s.io/kubevirt-machine-namespace=$TENANT_CLUSTER_NAMESPACE -n $TENANT_CLUSTER_NAMESPACE

echo "Installing networking (calico)"
./kubevirtci install-calico

echo "Enable hotplug"
#Add the feature gate to the resource of type Kubevirt.
./kubevirtci kubectl patch -n kubevirt kubevirt.kubevirt.io kubevirt  -p '{"spec":  { "configuration": { "developerConfiguration": { "featureGates": ["HotplugVolumes" ] }}}}' -o json --type merge

# enables insecure registry
for vmi in $(./kubevirtci kubectl get vmi -A --no-headers | awk '{ print $2 }')
do
        cat hack/vmi-insecure-registry | ./kubevirtci ssh-tenant $vmi $TENANT_CLUSTER_NAMESPACE
done

