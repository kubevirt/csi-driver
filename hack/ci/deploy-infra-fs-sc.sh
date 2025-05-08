#!/usr/bin/env bash

set -ex

source hack/common.sh

cat <<EOF | _kubectl_tenant apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: infra-fs
provisioner: csi.kubevirt.io
allowVolumeExpansion: true
parameters:
  infraStorageClassName: nfs-csi
  bus: scsi
EOF
