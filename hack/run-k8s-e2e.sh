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
export TENANT_CLUSTER_NAME=${TENANT_CLUSTER_NAME:-kvcluster}
export TENANT_CLUSTER_NAMESPACE=${TENANT_CLUSTER_NAMESPACE:-kvcluster}
export KUBEVIRTCI_TAG=${KUBEVIRTCI_TAG:-2405151527-09bcd71}

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
    ./kubevirtci kubectl create configmap -n $TENANT_CLUSTER_NAMESPACE $test_driver_cm --from-file=./hack/test-driver.yaml --from-file=./hack/test-driver-rwx.yaml
}

function create_capk_secret {
    echo "Creating ssh secret"
    # Find ssh key to connect
    ./kubevirtci kubectl get secret -n $TENANT_CLUSTER_NAMESPACE kvcluster-ssh-keys -o jsonpath='{.data}' | grep key | awk -F '"' '{print $4}' | base64 -d > ./capk.pem
    chmod 600 ./capk.pem
    ./kubevirtci kubectl create secret generic -n $TENANT_CLUSTER_NAMESPACE $capk_secret --from-file=./capk.pem
    rm -f ./capk.pem || true
}

# In order to support ReadWriteOncePod, we need to install the resize side car which we are not using right now. See https://kubernetes.io/blog/2021/09/13/read-write-once-pod-access-mode-alpha/ for more info
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
    image: quay.io/centos/centos:stream8
    securityContext:
      allowPrivilegeEscalation: false
      runAsNonRoot: true
      runAsUser: 1000
      capabilities:
        drop: ["ALL"]
      seccompProfile:
        type: "RuntimeDefault"
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
      cd /tmp
      curl --location https://dl.k8s.io/v1.30.1/kubernetes-test-linux-amd64.tar.gz | tar --strip-components=3 -zxf - kubernetes/test/bin/e2e.test kubernetes/test/bin/ginkgo
      chmod +x e2e.test
      curl -LO "https://dl.k8s.io/release/v1.30.1/bin/linux/amd64/kubectl"
      chmod +x kubectl
      echo \${TEST_DRIVER_PATH}
      ./e2e.test -kubeconfig \${KUBECONFIG} \
            -kubectl-path ./kubectl \
            -ginkgo.v \
            -ginkgo.no-color \
            -ginkgo.timeout=2h \
            -ginkgo.focus='External.Storage.*csi.kubevirt.io.*' \
            -ginkgo.skip='CSI Ephemeral-volume*' \
            -ginkgo.skip='SELinuxMountReadWriteOncePod.*' \
            -storage.testdriver=\${TEST_DRIVER_PATH}/test-driver.yaml \
            -provider=local -report-dir=/tmp
      ret1=\$?
      if [[ \${ret1} -ne 0 ]]; then
        echo "kubernetes e2e test failed"
      fi
      ./e2e.test -kubeconfig \${KUBECONFIG} \
            -kubectl-path ./kubectl \
            -ginkgo.v \
            -ginkgo.no-color \
            -ginkgo.timeout=2h \
            -ginkgo.focus='External.Storage.*csi.kubevirt.io.*should concurrently access the single volume from pods on different node.*' \
            -ginkgo.skip='CSI Ephemeral-volume*' \
            -ginkgo.skip='SELinuxMountReadWriteOncePod' \
            -ginkgo.skip='\((?:xfs|filesystem volmode|ntfs|ext4)\).* multiVolume \[Slow]' \
            -storage.testdriver=\${TEST_DRIVER_PATH}/test-driver-rwx.yaml \
            -report-prefix="rwx_" \
            -provider=local -report-dir=/tmp
      ret2=\$?
      if [[ \${ret2} -ne 0 ]]; then
        echo "kubernetes e2e RWX test failed"
      fi
      while [ ! -f /tmp/exit.txt ]; do
        sleep 2
      done
      if [[ \${ret1} -ne 0 ]]; then
         exit \ret1
       fi
       exit \$ret2
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

function patch_local_storage_profile() {
if ./kubevirtci kubectl get storageprofile local; then
  ./kubevirtci kubectl patch storageprofile local --type='merge' -p '{"spec":{"claimPropertySets":[{"accessModes":["ReadWriteOnce"], "volumeMode": "Filesystem"}]}}'
fi
}

function make_control_plane_schedulable() {
  for node in $(./kubevirtci kubectl-tenant get nodes -l node-role.kubernetes.io/control-plane -o custom-columns=:.metadata.name --no-headers 2>/dev/null | tail -n +2); do
    ./kubevirtci kubectl-tenant patch node --type=json -p '[{"op": "remove", "path": "/spec/taints"}]' "${node}" | tail -n +2 || true
  done
}

trap cleanup EXIT SIGSTOP SIGKILL SIGTERM
ensure_cluster_up
ensure_synced
make_control_plane_schedulable
create_test_driver_cm
create_capk_secret
patch_local_storage_profile
# DEBUG
echo "Printing debugging manifests PRE run"
./kubevirtci kubectl get nodes -o yaml
./kubevirtci kubectl-tenant get nodes -o yaml
./kubevirtci kubectl get vm -A -o yaml
./kubevirtci kubectl get vmi -A -o yaml
./kubevirtci kubectl-tenant get clusterrole snapshot-controller-runner -o yaml
./kubevirtci kubectl-tenant get clusterrolebinding snapshot-controller-role -o yaml
./kubevirtci kubectl-tenant get sa -n kube-system snapshot-controller -o yaml
# DEBUG
start_test_pod
# Wait for pod to be ready before getting logs
./kubevirtci kubectl wait pods -n $TENANT_CLUSTER_NAMESPACE ${test_pod} --for condition=Ready --timeout=180s
./kubevirtci kubectl logs -fn $TENANT_CLUSTER_NAMESPACE ${test_pod} >&1 &

while [[ ! $(./kubevirtci kubectl exec -n $TENANT_CLUSTER_NAMESPACE ${test_pod} -- ls /tmp/junit_01.xml 2>/dev/null) ]]; do
  sleep 30
done

while [[ ! $(./kubevirtci kubectl exec -n $TENANT_CLUSTER_NAMESPACE ${test_pod} -- ls /tmp/junit_rwx_01.xml 2>/dev/null) ]]; do
  sleep 30
done

if [[ -n "$ARTIFACTS" ]]; then
  echo "Copying results"
  ./kubevirtci kubectl cp "${TENANT_CLUSTER_NAMESPACE}/${test_pod}:/tmp/junit_01.xml" "${ARTIFACTS}/junit.functest.xml"
  ./kubevirtci kubectl cp "${TENANT_CLUSTER_NAMESPACE}/${test_pod}:/tmp/junit_rwx_01.xml" "${ARTIFACTS}/junit.functest-rwx.xml"
fi

./kubevirtci kubectl exec -n $TENANT_CLUSTER_NAMESPACE ${test_pod} -- touch /tmp/exit.txt
sleep 5

exit_code=$(./kubevirtci kubectl get pod -n $TENANT_CLUSTER_NAMESPACE ${test_pod} --output="jsonpath={.status.containerStatuses[].state.terminated.exitCode}")
# Make sure its a number
exit_code=$(($exit_code + 0))
if [[ $exit_code -ne 0 ]]; then
  # debug failing run
  echo "Printing debugging manifests POST run"
  ./kubevirtci kubectl get nodes -o yaml
  ./kubevirtci kubectl-tenant get nodes -o yaml
  ./kubevirtci kubectl get vm -A -o yaml
  ./kubevirtci kubectl get vmi -A -o yaml
  ./kubevirtci kubectl-tenant get clusterrole snapshot-controller-runner -o yaml
  ./kubevirtci kubectl-tenant get clusterrolebinding snapshot-controller-role -o yaml
  ./kubevirtci kubectl-tenant get sa -n kube-system snapshot-controller -o yaml
fi
exit $exit_code
