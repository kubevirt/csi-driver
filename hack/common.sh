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

KUBEVIRT_PROVIDER=${KUBEVIRT_PROVIDER:-k8s-1.17}
BASE_PATH=${KUBEVIRTCI_CONFIG_PATH:-$PWD}
## KUBEVIRTCI_PATH=$(kubevirtci::path)
CMD=${CMD:-}
KUBECTL=${KUBECTL:-}
TEST_PATH="tests/functional"
TEST_OUT_PATH=${TEST_PATH}/_out
JOB_TYPE=${JOB_TYPE:-}


KUBECTL=$(which kubectl 2> /dev/null) || true

if [ -z "${CMD}" ]; then
    if [ -z "${KUBECTL}" ] ; then
        CMD=oc
    else
        CMD=kubectl
    fi
fi
