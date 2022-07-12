FROM registry.ci.openshift.org/openshift/release:golang-1.18 AS builder
WORKDIR /src/kubevirt-csi-driver
COPY . .
RUN make build

FROM quay.io/centos/centos:stream9
LABEL maintainers="The KubeVirt Project <kubevirt-dev@googlegroups.com>"
LABEL description="KubeVirt CSI Driver"

RUN dnf install -y e2fsprogs xfsprogs && dnf clean all
COPY --from=builder /src/kubevirt-csi-driver/kubevirt-csi-driver .

ENTRYPOINT ["./kubevirt-csi-driver"]
