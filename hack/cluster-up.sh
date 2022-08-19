#!/usr/bin/env bash

set -euo pipefail

# ******************************************************
# Start infra cluster with tenant cluster
# ******************************************************
echo "Starting base cluster"
./kubevirtci up
echo "Installing capk"
./kubevirtci install-capk
echo "Creating kvcluster"
./kubevirtci create-cluster

echo "Waiting for kvcluster vmis to be ready"
#export KUBECONFIG=$(./kubevirtci kubeconfig)
./kubevirtci kubectl wait --for=condition=Ready vmi -l capk.cluster.x-k8s.io/kubevirt-machine-namespace=kvcluster -n kvcluster

echo "Installing networking (calico)"
./kubevirtci install-calico

echo "Enable hotplug"
#Add the feature gate to the resource of type Kubevirt.
./kubevirtci kubectl patch -n kubevirt kubevirt.kubevirt.io kubevirt  -p '{"spec":  { "configuration": { "developerConfiguration": { "featureGates": ["HotplugVolumes" ] }}}}' -o json --type merge

# TODO: update cluster VMI's with insecure registry...
# need to run pod per node, and apply change and restart
# ./kubevirtci kubectl apply -f ./deploy/host-pod.yaml
# ./kubevirtci kubectl-tenant exec host-pod -- /bin/sh -c "`cat ./deploy/containerd-config.sh`"
# sudo systemctl restart containerd

