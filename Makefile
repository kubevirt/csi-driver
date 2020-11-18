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

BINDIR=bin
REPO?=kubevirt
IMAGE?=csi-driver
TAG?=latest
REV=$(shell git describe --long --tags --match='v*' --always --dirty)

all: image

.PHONY: test
test:
	go test -v ./pkg/... ./cmd/... -coverprofile cover.out
	hack/run-lint-checks.sh

.PHONY: build
build:
	go build -o $(BINDIR)/kubevirt-csi-driver -ldflags '-X version.Version=$(REV)' cmd/kubevirt-csi-driver/kubevirt-csi-driver.go

.PHONY: verify
verify: fmt vet

.PHONY: fmt
fmt:
	hack/verify-gofmt.sh

.PHONY: vet
vet:
	hack/verify-govet.sh

.PHONY: image
image:
	docker build . -f Dockerfile -t $(REPO)/$(IMAGE):$(TAG)

.PHONY: push
push: image
	docker push $(REPO)/$(IMAGE):$(TAG)

.PHONY: vendor
vendor:
	go mod tidy
	go mod vendor
	go mod verify
