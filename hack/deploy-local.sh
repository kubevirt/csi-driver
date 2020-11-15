#!/bin/bash

# Deploy the operator on a local cluster (reachable by kubectl).

PROJECT_ROOT=$(dirname "${BASH_SOURCE}")/..

if which kubectl &>/dev/null; then
    CLI=kubectl
elif which oc &>/dev/null; then
    CLI=oc
else
    echo "Cannot find kubectl nor oc"
    exit 1
fi

$CLI apply -f "${PROJECT_ROOT}/deploy/prerequisites/"
$CLI apply -f "${PROJECT_ROOT}/deploy/operator.yaml"
