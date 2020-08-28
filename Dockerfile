FROM registry.fedoraproject.org/fedora-minimal:32
LABEL maintainers="The KubeVirt Project <kubevirt-dev@googlegroups.com>"
LABEL description="KubeVirt CSI Driver"

ARG binary=./_out/kubevirt-csi-driver

COPY ${binary} /kubevirt-csi-driver

ENTRYPOINT ["/kubevirt-csi-driver"]
