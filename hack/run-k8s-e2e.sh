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
export KUBEVIRT_PROVIDER=k8s-1.23
export TENANT_CLUSTER_NAME=${TENANT_CLUSTER_NAME:-kvcluster}
export TENANT_CLUSTER_NAMESPACE=${TENANT_CLUSTER_NAMESPACE:-kvcluster}
export KUBEVIRTCI_TAG=${KUBEVIRTCI_TAG:-2205231118-f12b50e}
export KUBEVIRT_PROVIDER=${KUBEVIRT_PROVIDER:-k8s-1.23}

test_pod=${TENANT_CLUSTER_NAME}-k8s-e2e-suite-runnner
test_driver_cm=${TENANT_CLUSTER_NAME}-test-driver
capk_secret=${TENANT_CLUSTER_NAME}-capk

export CLUSTERCTL_PATH=${CLUSTERCTL_PATH:-${_default_clusterctl_path}}

function cleanup {
    ./kubevirtci kubectl delete pod --wait=false --ignore-not-found=true -n $TENANT_CLUSTER_NAMESPACE $test_pod > /dev/null 2>&1
    ./kubevirtci kubectl delete cm --ignore-not-found=true -n $TENANT_CLUSTER_NAMESPACE $test_driver_cm > /dev/null 2>&1
    ./kubevirtci kubectl delete secret --ignore-not-found=true -n $TENANT_CLUSTER_NAMESPACE $capk_secret > /dev/null 2>&1
    rm -f ./capk.pem || true
}

function ensure_cluster_up {
    ./kubevirtci kubectl get ns || ret=$?
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

function create_test_driver_cm {
    echo "Creating test-driver CM"
    ./kubevirtci kubectl create configmap -n $TENANT_CLUSTER_NAMESPACE $test_driver_cm --from-file=./hack/test-driver.yaml
}

function create_capk_secret {
    echo "Creating ssh secret"
    # Find ssh key to connect
    ./kubevirtci kubectl get secret -n $TENANT_CLUSTER_NAMESPACE kvcluster-ssh-keys -o jsonpath='{.data}' | grep key | awk -F '"' '{print $4}' | base64 -d > ./capk.pem
    chmod 600 ./capk.pem
    ./kubevirtci kubectl create secret generic -n $TENANT_CLUSTER_NAMESPACE $capk_secret --from-file=./capk.pem
    rm -f ./capk.pem || true
}

function start_test_pod {
cat <<EOF | ./kubevirtci kubectl create -f -
apiVersion: v1
kind: Pod
metadata:
  name: ${test_pod}
  namespace: ${TENANT_CLUSTER_NAMESPACE}
spec:
  restartPolicy: Never
  containers:
  - name: test-suite
    image: registry.access.redhat.com/ubi8/ubi:8.0
    env:
    - name: KUBECONFIG
      value: /etc/kubernetes/kubeconfig/value
    - name: TEST_DRIVER_PATH
      value: "/etc/test-driver"
    - name:  KUBE_SSH_USER
      value: capk
    - name: KUBE_SSH_KEY_PATH
      value: /capk/capk.pem
    command:
    - /bin/bash
    - -c
    - |
      curl --location https://dl.k8s.io/v1.22.0/kubernetes-test-linux-amd64.tar.gz |   tar --strip-components=3 -zxf - kubernetes/test/bin/e2e.test kubernetes/test/bin/ginkgo
      chmod +x e2e.test
      curl -LO "https://dl.k8s.io/release/v1.22.0/bin/linux/amd64/kubectl"
      chmod +x kubectl
      echo \$TEST_DRIVER_PATH
      ./e2e.test -kubeconfig \${KUBECONFIG} -kubectl-path ./kubectl -ginkgo.v -ginkgo.focus='External.Storage.*csi.kubevirt.io.*' -ginkgo.skip='CSI Ephemeral-volume*' -storage.testdriver=\${TEST_DRIVER_PATH}/test-driver.yaml -provider=local
    volumeMounts:
    - name: kubeconfig
      mountPath: "/etc/kubernetes/kubeconfig"
      readOnly: true
    - name: test-driver-config
      mountPath: "/etc/test-driver"
      readOnly: true
    - name: capk
      mountPath: "/capk"
      readOnly: true
  volumes:
  - name: kubeconfig
    secret:
      secretName: ${TENANT_CLUSTER_NAME}-kubeconfig
  - name: test-driver-config
    configMap:
      name: ${test_driver_cm}
  - name: capk
    secret:
      secretName: ${capk_secret}
EOF
}

trap cleanup EXIT SIGSTOP SIGKILL SIGTERM
ensure_cluster_up
ensure_synced
create_test_driver_cm
create_capk_secret
start_test_pod
# Wait for pod to be ready before getting logs
./kubevirtci kubectl wait pods -n $TENANT_CLUSTER_NAMESPACE ${test_pod} --for condition=Ready --timeout=180s
./kubevirtci kubectl logs -fn $TENANT_CLUSTER_NAMESPACE ${test_pod}

exit_code=$(./kubevirtci kubectl get pod -n $TENANT_CLUSTER_NAMESPACE ${test_pod} --output="jsonpath={.status.containerStatuses[].state.terminated.exitCode}")
# Make sure its a number
exit_code=$(($exit_code + 0))
exit $exit_code
