#!/bin/bash            
                                 
set -e -o pipefail

TOOLS_DIR=${TOOLS_DIR:-$PWD/hack/tools}
CLUSTERCTL_PATH=${TOOLS_DIR}/bin/clusterctl
KUBECTL_PATH=${TOOLS_DIR}/bin/kubectl
TEST_WORKING_DIR=${TOOLS_DIR}/e2e-test-workingdir
export ARTIFACTS=${ARTIFACTS:-k8s-reporter}
export KUBECONFIG=$(./kubevirtci kubeconfig)                                                                
export INFRA_CLUSTER_NAMESPACE=${INFRA_CLUSTER_NAMESPACE:-kvcluster}
mkdir -p $ARTIFACTS

if [ ! -f "${CLUSTERCTL_PATH}" ]; then
        echo >&2 "Downloading clusterctl ..."
        curl -L https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.0.0/clusterctl-linux-amd64 -o ${CLUSTERCTL_PATH}
        chmod u+x ${CLUSTERCTL_PATH}
fi      

if [ ! -f "${KUBECTL_PATH}" ]; then
        echo >&2 "Downloading kubectl ..."
        curl -L "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" -o ${KUBECTL_PATH}
        chmod u+x ${KUBECTL_PATH}
fi

go get github.com/onsi/ginkgo/v2
go mod vendor
go install github.com/onsi/ginkgo/v2/ginkgo

rm -rf $TEST_WORKING_DIR
mkdir -p $TEST_WORKING_DIR
ginkgo -p -procs=4 $BIN_DIR/e2e.test -- -ginkgo.v -test.v -ginkgo.no-color --kubectl-path $KUBECTL_PATH --clusterctl-path $CLUSTERCTL_PATH  --working-dir $TEST_WORKING_DIR --infra-kubeconfig=$KUBECONFIG --infra-cluster-namespace=${INFRA_CLUSTER_NAMESPACE}

