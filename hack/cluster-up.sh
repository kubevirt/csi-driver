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

