#!/bin/bash            

set -e -o pipefail

if [[ -n "$ARTIFACTS" ]]; then
  echo "Running inside CI"
  ginkgo_args="--ginkgo.junit-report=$ARTIFACTS/junit.functest.xml --ginkgo.no-color"
fi

go test -o $PWD/_out/sanity.test -c -v $PWD/sanity/...
$PWD/_out/sanity.test $ginkgo_args
