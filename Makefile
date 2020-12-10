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

#.PHONY: build
#build:
#	go build -o $(BINDIR)/kubevirt-csi-driver -ldflags '-X version.Version=$(REV)' cmd/kubevirt-csi-driver/kubevirt-csi-driver.go

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
	podman build . -f Dockerfile -t $(REPO)/$(IMAGE):$(TAG)

.PHONY: push
push: image
	podman push $(REPO)/$(IMAGE):$(TAG)

.PHONY: vendor
vendor:
	go mod tidy
	go mod vendor
	go mod verify

.PHONY: mockgen
mockgen:
	mockgen -source=pkg/kubevirt/client.go -destination=pkg/kubevirt/mocked_client.go -package=kubevirt

test-functional:
	ginkgo

SHELL :=/bin/bash

TARGET_NAME=kubevirt-csi-driver
IMAGE_REF=quay.io/kubevirt/$(TARGET_NAME):latest
GO_TEST_PACKAGES :=./pkg/... ./cmd/...
IMAGE_REGISTRY?=registry.svc.ci.openshift.org

# You can customize go tools depending on the directory layout.
# example:
#GO_BUILD_PACKAGES :=./pkg/...
# You can list all the golang related variables by:
#   $ make -n --print-data-base | grep ^GO

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/deps-gomod.mk \
	targets/openshift/images.mk \
	targets/openshift/bindata.mk \
)

# All the available targets are listed in <this-file>.help
# or you can list it live by using `make help`


# You can list all codegen related variables by:
#   $ make -n --print-data-base | grep ^CODEGEN

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context
# It will generate target "image-$(1)" for builing the image an binding it as a prerequisite to target "images".
$(call build-image,$(TARGET_NAME),$(IMAGE_REF),./Dockerfile,.)

# This will call a macro called "add-bindata" which will generate bindata specific targets based on the parameters:
# $0 - macro name
# $1 - target suffix
# $2 - input dirs
# $3 - prefix
# $4 - pkg
# $5 - output
# It will generate targets {update,verify}-bindata-$(1) logically grouping them in unsuffixed versions of these targets
# and also hooked into {update,verify}-generated for broader integration.
$(call add-bindata,generated,./deploy/...,assets,generated,pkg/generated/bindata.go)

