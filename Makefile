# Copyright 2020 The KubeVirt-csi Authors.
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

.PHONY: cluster-up cluster-down cluster-sync cluster-clean

DOCKER_REPO?=kubevirt
ARTIFACTS_PATH?=_out
IMAGE?=kubevirt-csi-driver
TAG?=latest

all: image

build: clean
	CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' -o $(ARTIFACTS_PATH)/kubevirt-csi-driver cmd/main.go

image: build
	docker build -t $(DOCKER_REPO)/$(IMAGE):$(TAG) -f Dockerfile .

push: image
	docker push $(DOCKER_REPO)/$(IMAGE):$(TAG)

clean:
	rm -rf $(ARTIFACTS_PATH)

.PHONY: test
test:
	go test -v ./cmd/... ./pkg/...
	hack/run-lint-checks.sh
