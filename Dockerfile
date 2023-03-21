ARG builder_image=docker.io/library/golang:1.19.7
FROM ${builder_image} AS builder
WORKDIR /src/kubevirt-csi-driver
COPY . .
RUN make build

FROM quay.io/centos/centos:stream9
ARG git_url=https://github.com/kubevirt/csi-driver.git

LABEL maintainers="The KubeVirt Project <kubevirt-dev@googlegroups.com>" \
      description="KubeVirt CSI Driver" \
      multi.GIT_URL=${git_url}

ENTRYPOINT ["./kubevirt-csi-driver"]

RUN dnf install -y e2fsprogs xfsprogs && dnf clean all

ARG git_sha=NONE
LABEL multi.GIT_SHA=${git_sha}

COPY --from=builder /src/kubevirt-csi-driver/kubevirt-csi-driver .
