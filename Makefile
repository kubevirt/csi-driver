#
# Copyright 2020 The KubeVirt-csi Authors.
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


SHELL :=/bin/bash

TARGET_NAME = kubevirt-csi-driver
REGISTRY ?= quay.io/kubevirt
TAG ?= latest
IMAGE_REF=$(REGISTRY)/$(TARGET_NAME):$(TAG)
GO_TEST_PACKAGES :=./pkg/... ./cmd/...
IMAGE_REGISTRY?=registry.svc.ci.openshift.org
SHA := $(shell git describe --no-match  --always --abbrev=40 --dirty)
BIN_DIR := bin

export KUBEVIRT_PROVIDER

# You can customize go tools depending on the directory layout.
# example:
#GO_BUILD_PACKAGES :=./pkg/...
# You can list all the golang related variables by:
#   $ make -n --print-data-base | grep ^GO

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/deps-gomod.mk \
	targets/openshift/bindata.mk \
)

# All the available targets are listed in <this-file>.help
# or you can list it live by using `make help`


# You can list all codegen related variables by:
#   $ make -n --print-data-base | grep ^CODEGEN
.PHONY: image-build
# let's disable generate for for now
# it updates libs and I think it is better to do that manually
# especially when changes will be backported
#image-build: generate
image-build:
	source ./hack/cri-bin.sh && \
	$$CRI_BIN build -t $(IMAGE_REF) --build-arg git_sha=$(SHA) .



.PHONY: image-push
image-push:
	source ./hack/cri-bin.sh && \
	$$CRI_BIN push $$PUSH_FLAGS $(IMAGE_REF)

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

.PHONY: cluster-up
cluster-up:
	./hack/cluster-up.sh

.PHONY: cluster-down
cluster-down:
	./kubevirtci down

# This deploys both controller and ds in tenant
.PHONY: cluster-sync
cluster-sync:
	./hack/cluster-sync.sh

# This deploys the controller in the infra cluster and ds in tenant
.PHONY: cluster-sync-split
cluster-sync-split:
	./hack/cluster-sync-split.sh

.PHONY: kubevirt-deploy
kubevirt-deploy:
	sh -c "./hack/kubevirt-deploy.sh"

.PHONY: mockgen
mockgen:
	mockgen -source=pkg/kubevirt/client.go -destination=pkg/kubevirt/mocked_client.go -package=kubevirt

.PHONY: kubeconfig
kubeconfig:
	@ if [ -n "${KUBECONFIG}" ]; then echo ${KUBECONFIG}; else $(MAKE) cluster-up kubevirt-deploy && ./cluster-up/kubeconfig.sh; fi

.PHONY: linter
linter:
	./hack/run-linter.sh

.PHONY: build-e2e-test
build-e2e-test: ## Builds the test binary
	BIN_DIR=$(BIN_DIR) ./hack/build-e2e.sh

.PHONY: e2e-test
e2e-test: build-e2e-test ## run e2e tests
	BIN_DIR=$(BIN_DIR) ./hack/run-e2e.sh

.PHONY: sanity-test
sanity-test:
	./hack/run-sanity.sh

.PHONY: generate
generate:
	./hack/generate_clients.sh
