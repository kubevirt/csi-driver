#!/usr/bin/env bash

set -eo pipefail

source /etc/profile.d/gimme.sh

export PATH=${GOPATH}/bin:$PATH

eval "$@"

if [ -n ${RUN_UID} ] && [ -n ${RUN_GID} ]; then
    find . -user root -exec chown -h ${RUN_UID}:${RUN_GID} {} \;
fi
