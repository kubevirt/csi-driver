#!/usr/bin/env bash
set -ex

go get kubevirt.io/api
client-gen --input-base="kubevirt.io/api/" --input="core/v1" --output-package="kubevirt.io/csi-driver/pkg/generated/kubevirt/client-go/clientset" --output-base="../../" --clientset-name="versioned" --go-header-file hack/boilerplate.go.txt
go get kubevirt.io/containerized-data-importer-api
client-gen --input-base=kubevirt.io/containerized-data-importer-api/pkg/apis --input=core/v1beta1 --output-package=kubevirt.io/csi-driver/pkg/generated/containerized-data-importer/client-go/clientset --output-base=../../ --clientset-name=versioned --go-header-file hack/boilerplate.go.txt
