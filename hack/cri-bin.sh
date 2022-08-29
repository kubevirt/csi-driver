#!/usr/bin/env bash

set -ex

if podman ps >/dev/null; then
    _cri_bin=podman
    >&2 echo "selecting podman as container runtime"
    if [[ ${REGISTRY} == 127.0.0.1* ]]; then
      echo "not verifying tls, registry contains localhost"
      _insecure_registry="--tls-verify=false"
    fi
elif docker ps >/dev/null; then
    _cri_bin=docker
    >&2 echo "selecting docker as container runtime"
else
    >&2 echo "no working container runtime found. Neither docker nor podman seems to work."
    exit 1
fi

CRI_BIN=${_cri_bin}
PUSH_FLAGS=${_insecure_registry}

