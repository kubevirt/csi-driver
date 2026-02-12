#!/usr/bin/env bash
#
# This file is part of the KubeVirt project
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Copyright 2017 Red Hat, Inc.
#

set -e

## source cluster/kubevirtci.sh

CSI_DRIVER_NAMESPACE=${CSI_DRIVER_NAMESPACE:-kubevirt-csi-driver}
BASE_PATH=${KUBEVIRTCI_CONFIG_PATH:-$PWD}
CMD=${CMD:-}
KUBECTL=${KUBECTL:-}
TEST_PATH="tests/functional"
TEST_OUT_PATH=_out
JOB_TYPE=${JOB_TYPE:-}


KUBECTL=$(which kubectl 2> /dev/null) || true

if [ -z "${CMD}" ]; then
    if [ -z "${KUBECTL}" ] ; then
        CMD=oc
    else
        CMD=kubectl
    fi
fi

get_latest_release() {
  curl -s "https://api.github.com/repos/$1/releases/latest" |       # Get latest release from GitHub api
    grep '"tag_name":' |                                            # Get tag line
    sed -E 's/.*"([^"]+)".*/\1/'                                    # Pluck JSON value (avoid jq)
}

function _kubectl() {
    ./kubevirtci kubectl "$@"
}

function _kubectl_tenant() {
    ./kubevirtci kubectl-tenant "$@"
}


function cluster::generate_driver_configmap_overlay() {
cat <<- END > ./deploy/$1/dev-overlay/infra-namespace-configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: driver-config
  namespace: kubevirt-csi-driver
data:
  infraClusterNamespace: $TENANT_CLUSTER_NAMESPACE
  infraClusterLabels: csi-driver/cluster=tenant
END
}

function cluster::generate_storageclass_overlay() {
# ./kubevirtci kubectl get sc -o jsonpath={.items[?(@.metadata.annotations."storageclass\.kubernetes\.io/is-default-class")].metadata.name}
# ^^^^ gets default storage class, but can't seem to store it properly in a variable, and there is no guarantee a default exists.

cat <<- END > ./deploy/$1/dev-overlay/storageclass.yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: kubevirt
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: csi.kubevirt.io
allowVolumeExpansion: true
parameters:
  infraStorageClassName: $2
  bus: scsi
END
}

function cluster::generate_tenant_dev_kustomization() {
  cat <<- END > ./deploy/tenant/dev-overlay/kustomization.yaml
resources:
- ../base
namespace: $CSI_DRIVER_NAMESPACE
patches:
- path: infra-namespace-configmap.yaml
- path: node.yaml
- path: storageclass.yaml
END
}

function cluster::generate_controller_dev_kustomization() {
  cat <<- END > ./deploy/$1/dev-overlay/kustomization.yaml
resources:
- ../base
namespace: $2
patches:
- path: controller.yaml
END
}

function tenant::deploy_csidriver_namespace() {
  cat <<- END | ./kubevirtci kubectl-tenant apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: $1
  labels:
    name: $1
END
}

function cluster::patch_local_storage_profile() {
if ./kubevirtci kubectl get storageprofile local; then
  ./kubevirtci kubectl patch storageprofile local --type='merge' -p '{"spec":{"claimPropertySets":[{"accessModes":["ReadWriteOnce"], "volumeMode": "Filesystem"}]}}'
fi
}

function tenant::deploy_snapshotresources() {
  ./kubevirtci kubectl-tenant apply -f ./deploy/tenant/base/rbac-snapshot-controller.yaml
  ./kubevirtci kubectl-tenant apply -f ./deploy/tenant/base/setup-snapshot-controller.yaml
  ./kubevirtci kubectl-tenant apply -f ./deploy/tenant/base/snapshot.storage.k8s.io_volumesnapshotclasses.yaml
  ./kubevirtci kubectl-tenant apply -f ./deploy/tenant/base/snapshot.storage.k8s.io_volumesnapshotcontents.yaml
  ./kubevirtci kubectl-tenant apply -f ./deploy/tenant/base/snapshot.storage.k8s.io_volumesnapshots.yaml
  # Make sure the infra rbd snapshot class is the default snapshot class
  ./kubevirtci kubectl patch volumesnapshotclass csi-rbdplugin-snapclass --type merge -p '{"metadata": {"annotations":{"snapshot.storage.kubernetes.io/is-default-class":"true"}}}'
}

function wait::check_rollout() {
    local KUBE_CMD="$1"
    local RES_TYPE="$2"
    local RES_NAME="$3"
    local NS="$4"
    local TIMEOUT="$5"

    if ! $KUBE_CMD rollout status "$RES_TYPE/$RES_NAME" -n "$NS" --timeout="$TIMEOUT"; then
        echo "Error: Rollout failed"

        # 1. Describe the failing resource
        $KUBE_CMD describe "$RES_TYPE/$RES_NAME" -n "$NS"

        # 2. Get Pods specifically for this app
        # We assume the resource name matches part of the pod name.
        $KUBE_CMD get pods -n "$NS" -o wide | grep "$RES_NAME"

        # 3. Fetch logs from the first failing pod found
        local POD_NAME=$($KUBE_CMD get pods -n "$NS" | grep "$RES_NAME" | awk '{print $1}' | head -n 1)

        if [ -n "$POD_NAME" ]; then
            echo "Logs for pod: $POD_NAME"
            $KUBE_CMD describe pod "$POD_NAME" -n "$NS"
            $KUBE_CMD logs "$POD_NAME" -n "$NS" --all-containers --previous --tail=50 || true
            $KUBE_CMD logs "$POD_NAME" -n "$NS" --all-containers --tail=50
        else
            echo "No pods found for $RES_NAME to fetch logs from."
        fi

        # Exit with failure
        exit 1
    fi
}