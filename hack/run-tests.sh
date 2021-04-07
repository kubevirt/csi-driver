#!/usr/bin/env bash

set -euo pipefail

source hack/common.sh
## source cluster/kubevirtci.sh


if [[ ${JOB_TYPE} = "prow" ]]; then
    export KUBECTL_BINARY="oc"
else
    export KUBECTL_BINARY="cluster/kubectl.sh"
fi


./${TEST_OUT_PATH}/functional.test -ginkgo.v -kubeconfig="${KUBECONFIG}" -master=

if [ -f ${CSV_FILE} ]; then
  rm -f ${CSV_FILE}
fi

## # Check the webhook, to see if it allow updating of the HyperConverged CR
## ${KUBECTL_BINARY} patch hco -n kubevirt-hyperconverged kubevirt-hyperconverged -p '{"spec":{"infra":{"nodePlacement":{"tolerations":[{"effect":"NoSchedule","key":"key","operator":"Equal","value":"value"}]}}}}' --type=merge
## ${KUBECTL_BINARY} patch hco -n kubevirt-hyperconverged kubevirt-hyperconverged -p '{"spec":{"workloads":{"nodePlacement":{"tolerations":[{"effect":"NoSchedule","key":"key","operator":"Equal","value":"value"}]}}}}' --type=merge
## # Read the HyperConverged CR
## ${KUBECTL_BINARY} get hco -n kubevirt-hyperconverged kubevirt-hyperconverged -o yaml
## # Check the webhook, to see if it allow deleteing of the HyperConverged CR
## ${KUBECTL_BINARY} delete hco -n kubevirt-hyperconverged kubevirt-hyperconverged
