FROM registry.svc.ci.openshift.org/openshift/release:golang-1.15 AS builder
WORKDIR /src/kubevirt-csi-driver
COPY . .
RUN make build

FROM registry.fedoraproject.org/fedora-minimal:33
LABEL maintainers="The KubeVirt Project <kubevirt-dev@googlegroups.com>"
LABEL description="KubeVirt CSI Driver"

RUN microdnf install -y e2fsprogs xfsprogs && microdnf clean all
COPY --from=builder /src/kubevirt-csi-driver/kubevirt-csi-driver .

ENTRYPOINT ["./kubevirt-csi-driver"]
