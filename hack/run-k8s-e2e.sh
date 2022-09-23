#!/bin/bash
#Copyright 2022 The kubevirt-csi driver Authors.
#
#Licensed under the Apache License, Version 2.0 (the "License");
#you may not use this file except in compliance with the License.
#You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
#Unless required by applicable law or agreed to in writing, software
#distributed under the License is distributed on an "AS IS" BASIS,
#WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#See the License for the specific language governing permissions and
#limitations under the License.
set -e
export KUBECONFIG=$(cluster-up/cluster-up/kubeconfig.sh)
export TENANT_CLUSTER_NAME=${TENANT_CLUSTER_NAME:-kvcluster}
export TENANT_CLUSTER_NAMESPACE=${TENANT_CLUSTER_NAMESPACE:-kvcluster}
export KUBEVIRTCI_TAG=${KUBEVIRTCI_TAG:-2205231118-f12b50e}

_kubectl=cluster-up/cluster-up/kubectl.sh
_virtctl=./hack/tools/bin/virtctl
_default_clusterctl_path=./hack/tools/bin/clusterctl
_default_tmp_path=./hack/tools/bin/tmp

export CLUSTERCTL_PATH=${CLUSTERCTL_PATH:-${_default_clusterctl_path}}

function ensure_cluster_up {
    ${_kubectl} get ns || ret=$?
    echo "Return $ret"
    if [ -z "$ret" ]; then
	echo "Cluster running"
    else
        echo "Cluster not running, starting cluster"
	make cluster-up
    fi
}

function ensure_synced {
    # This appears to not work if run multiple times. The sync fails if run a second time.
    make cluster-sync-split
}

function get_control_plane_vm_name {
    vms_list=$(${_kubectl} get vm -n ${TENANT_CLUSTER_NAMESPACE} --no-headers -o custom-columns=":metadata.name")
    for vm in $vms_list
    do
        if [[ "$vm" == ${TENANT_CLUSTER_NAME}-control-plane* ]]; then
            control_plane_vm_name=$vm
	    return 0
        fi
    done
    echo "control-plane vm is not found in namespace ${TENANT_CLUSTER_NAMESPACE} (looking for regex ${TENANT_CLUSTER_NAME}-control-plane*)"
    exit 1
}

function start_tenant_api_forward {
    # Use the infra cluster kubeconfig from the environment variable otherwise the forward will fail
    echo "${_virtctl} port-forward -n $TENANT_CLUSTER_NAMESPACE vmi/$control_plane_vm_name 64443:6443"
    ${_virtctl} port-forward -n $TENANT_CLUSTER_NAMESPACE vmi/$control_plane_vm_name 64443:6443 > /dev/null 2>&1 &
    trap 'kill $(jobs -p) > /dev/null 2>&1' EXIT
}

function get_tenant_kubeconfig_for_e2e {
   echo "$CLUSTERCTL_PATH get kubeconfig ${TENANT_CLUSTER_NAME} -n ${TENANT_CLUSTER_NAMESPACE} > .${TENANT_CLUSTER_NAME}-kubeconfig-e2e"
   $CLUSTERCTL_PATH get kubeconfig ${TENANT_CLUSTER_NAME} -n ${TENANT_CLUSTER_NAMESPACE} > .${TENANT_CLUSTER_NAME}-kubeconfig-e2e
   # Modify the kubeconfig to achieve 3 things
   # 1. Change API ip address to 127.0.0.1:64443 (the port forward)
   # 2. Add insecure-skip-tls-verify: true
   # 3. Remove the CA which cannot be defined if insecure-skip-tls-verify: true is set
   # Need the insecure because the port-forward has changed the ip address and the cert doesn't know about 127.0.0.1
   sed -i 's/server:.*/server: https:\/\/127.0.0.1:64443/' .${TENANT_CLUSTER_NAME}-kubeconfig-e2e
   sed -i '/server:.*/a \ \ \ \ insecure-skip-tls-verify: true' .${TENANT_CLUSTER_NAME}-kubeconfig-e2e
   sed -i '/certificate-authority-data/d' .${TENANT_CLUSTER_NAME}-kubeconfig-e2e
   tenant_kubeconfig=.${TENANT_CLUSTER_NAME}-kubeconfig-e2e
}

function ensure_e2e_binary {
    if [ ! -f "e2e.test" ]; then
	# Would prefer to detect k8s version from tenant cluster.
        curl --location https://dl.k8s.io/v1.22.0/kubernetes-test-linux-amd64.tar.gz |   tar --strip-components=3 -zxf - kubernetes/test/bin/e2e.test kubernetes/test/bin/ginkgo
    else
	echo "Binary exists"
    fi
}

function enable_sshuttle {
    if ! command -v sshuttle &> /dev/null
    then
      #Setup sshutle
      dnf install -y sshuttle
    fi

    # Find ssh key to connect
    ${_kubectl} get secret -n $TENANT_CLUSTER_NAMESPACE kvcluster-ssh-keys -o jsonpath='{.data}' | grep key | awk -F '"' '{print $4}' | base64 -d > ./capk.pem
    chmod 600 ./capk.pem

    vmis_list=($(${_kubectl} get vmi -n ${TENANT_CLUSTER_NAMESPACE} --no-headers -o custom-columns=":metadata.name"))
    ssh_port=60022
    for vmi in ${vmis_list[@]};
    do
	# Install python 3.9 so sshuttle can connect properly. If the nodes had python3.9 on them already we wouldn't need to install it.
	echo "Installing python 3.9 on tenant nodes"
        ./kubevirtci ssh-tenant $vmi ${TENANT_CLUSTER_NAMESPACE} "sudo apt install software-properties-common -y && sudo add-apt-repository ppa:deadsnakes/ppa -y && sudo apt install python3.9 -y"
    done
    
    # Port forward for ssh
    echo " ${_virtctl} port-forward -n $TENANT_CLUSTER_NAMESPACE vmi/$control_plane_vm_nam $ssh_port:22"
    ${_virtctl} port-forward -n $TENANT_CLUSTER_NAMESPACE vmi/$control_plane_vm_name $ssh_port:22 > /dev/null 2>&1 &
    trap 'kill $(jobs -p) > /dev/null 2>&1' EXIT

    echo "Starting sshuttle"
    echo "sshuttle -r capk@127.0.0.1:${ssh_port} 10.244.196.0/24 -e 'ssh -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -i ./capk.pem'"
    sshuttle -r capk@127.0.0.1:${ssh_port} 10.244.196.0/24 -e 'ssh -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -i ./capk.pem' &
    trap 'kill $(jobs -p) > /dev/null 2>&1' EXIT
    echo "done"
}

ensure_cluster_up
ensure_synced
get_control_plane_vm_name
echo $control_plane_vm_name
start_tenant_api_forward
echo "API port forwarded"
get_tenant_kubeconfig_for_e2e
echo "Ensuring test binary exists"
ensure_e2e_binary
echo "Enabling sshuttle"
enable_sshuttle
echo "Starting test"
export KUBE_SSH_KEY_PATH=./capk.pem
export KUBE_SSH_USER=capk

# Skip CSI ephemeral volumes as the driver doesn't support them (yet)
./e2e.test -kubeconfig ${tenant_kubeconfig} -ginkgo.v -ginkgo.focus='External.Storage.*csi.kubevirt.io.*' -ginkgo.skip='CSI Ephemeral-volume*' -storage.testdriver=./hack/test-driver.yaml -provider=local

