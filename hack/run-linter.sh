#!/usr/bin/env bash
set -e

if [ ! -f "$(go env GOPATH)/bin/golangci-lint" ]; then
  # install golangci-lint
  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$(go env GOPATH)/bin"
fi

golangci-lint run --timeout=5m

if [ ! -f "$(go env GOPATH)/bin/ginkgolinter" ]; then
  # install ginkgolinter
  go install github.com/nunnatsa/ginkgolinter/cmd/ginkgolinter@latest
fi

ginkgolinter ./...
