#!/bin/sh
#
# Copyright 2021 The CDI Authors.
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

# based on: https://raw.githubusercontent.com/kubevirt/containerized-data-importer/main/hack/update-kubevirtci.sh

# the kubevirtci release to vendor from (https://github.com/kubevirt/kubevirtci/releases)
kubevirtci_release_tag=`curl -L -Ss https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirtci/latest`

# remove previous cluster-up dir entirely before vendoring
rm -rf ./cluster-up

# download and extract the cluster-up dir from a specific hash in kubevirtci
curl -L https://github.com/kubevirt/kubevirtci/archive/${kubevirtci_release_tag}/kubevirtci.tar.gz | tar xz kubevirtci-${kubevirtci_release_tag}/cluster-up --strip-component 1

echo "KUBEVIRTCI_TAG=${kubevirtci_release_tag}" >> ./cluster-up/hack/common.sh