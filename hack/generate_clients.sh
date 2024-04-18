#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

go install k8s.io/code-generator/cmd/client-gen@v0.28.6
client-gen --input-base="kubevirt.io/api/" --input="core/v1" --output-package="kubevirt.io/csi-driver/pkg/generated/kubevirt/client-go/clientset" --output-base="../../" --clientset-name="versioned" --go-header-file hack/boilerplate.go.txt

go get kubevirt.io/containerized-data-importer-api
client-gen --input-base=kubevirt.io/containerized-data-importer-api/pkg/apis --input=core/v1beta1 --output-package=kubevirt.io/csi-driver/pkg/generated/containerized-data-importer/client-go/clientset --output-base=../../ --clientset-name=versioned --go-header-file hack/boilerplate.go.txt

go get github.com/kubernetes-csi/external-snapshotter
client-gen --clientset-name versioned \
    --input-base github.com/kubernetes-csi/external-snapshotter/client/v6/apis \
    --input volumesnapshot/v1 \
    --output-base ../.. \
    --output-package kubevirt.io/csi-driver/pkg/generated/external-snapshotter/client-go/clientset \
    --go-header-file hack/boilerplate.go.txt

