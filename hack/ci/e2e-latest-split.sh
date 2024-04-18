#!/bin/bash

set -ex

KUBEVIRT_DEPLOY_NFS_CSI=true make cluster-up
make cluster-sync-split
./hack/ci/deploy-infra-fs-sc.sh
make e2e-test
