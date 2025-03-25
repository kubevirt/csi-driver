#!/usr/bin/env bash
set -e

GOLANGCI_VERSION="${GOLANGCI_VERSION:-v1.64.8}"

if [ ! -f "$(go env GOPATH)/bin/golangci-lint" ]; then
  # install golangci-lint
  go install "github.com/golangci/golangci-lint/cmd/golangci-lint@${GOLANGCI_VERSION}"
fi

golangci-lint run --verbose --timeout=10m

if [ ! -f "$(go env GOPATH)/bin/ginkgolinter" ]; then
  # install ginkgolinter
  go install github.com/nunnatsa/ginkgolinter/cmd/ginkgolinter@latest
fi

ginkgolinter ./...
