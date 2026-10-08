#!/usr/bin/env bash

#Copyright 2026 The csi driver Authors.
#
#Licensed under the Apache License, Version 2.0 (the "License");
#you may not use this file except in compliance with the License.
#You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
#Unless required by applicable law or agreed to in writing, software
#distributed under the License is distributed on an "AS IS" BASIS,
#WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#See the License for the specific language governing permissions and
#limitations under the License.

set -exuo pipefail

function cleanup_gh_install() {
    [ -n "${gh_cli_dir}" ] && [ -d "${gh_cli_dir}" ] && rm -rf "${gh_cli_dir:?}/"
}

function ensure_gh_cli_installed() {
    if command -V gh; then
        return
    fi

    trap 'cleanup_gh_install' EXIT SIGINT SIGTERM

    # install gh cli for uploading release artifacts, with prompt disabled to enforce non-interactive mode
    gh_cli_dir=$(mktemp -d)
    (
        cd  "$gh_cli_dir/"
        curl -sSL "https://github.com/cli/cli/releases/download/v${GH_CLI_VERSION}/gh_${GH_CLI_VERSION}_linux_amd64.tar.gz" -o "gh_${GH_CLI_VERSION}_linux_amd64.tar.gz"
        tar xvf "gh_${GH_CLI_VERSION}_linux_amd64.tar.gz"
    )
    export PATH="$gh_cli_dir/gh_${GH_CLI_VERSION}_linux_amd64/bin:$PATH"
    if ! command -V gh; then
        echo "gh cli not installed successfully"
        exit 1
    fi
    gh config set prompt disabled
}

function update_github_release() {
    # note: for testing purposes we set the target repository, gh cli seems to always automatically choose the
    # upstream repository automatically, even when you are in a fork

    set +e
    if ! gh release view --repo "$GITHUB_REPOSITORY" "$TAG" ; then
        set -e
        gh release create --repo "$GITHUB_REPOSITORY" "$TAG" --title="$TAG"
    else
        set -e
    fi

    gh release upload --repo "$GITHUB_REPOSITORY" --clobber "$TAG" \
        _out/release/*.yaml deploy/infra-cluster-service-account.yaml
}

function render_manifests() {
    # render each deploy base with the driver image pinned to the release tag
    for base in tenant controller-infra controller-tenant; do
        mkdir -p "_out/release/${base}"
        cat >"_out/release/${base}/kustomization.yaml" <<EOF
resources:
- ../../../deploy/${base}/base
images:
- name: ${DOCKER_REPO}/kubevirt-csi-driver
  newTag: ${TAG}
EOF
        kubectl kustomize "_out/release/${base}" >"_out/release/${base}.yaml"
    done
}

function main() {
    TAG="$(git tag --points-at HEAD | head -1)"
    if [ -z "$TAG" ]; then
        echo "commit $(git show -s --format=%h) doesn't have a tag, exiting..."
        exit 0
    fi

    export TAG

    GIT_ASKPASS="$(pwd)/hack/git-askpass.sh"
    [ -f "$GIT_ASKPASS" ] || exit 1
    export GIT_ASKPASS

    render_manifests

    ensure_gh_cli_installed

    gh auth login --with-token <"$GITHUB_TOKEN_PATH"

    update_github_release
}

main "$@"

