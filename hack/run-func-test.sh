#!/usr/bin/env bash

./kubevirtci up && ./kubevirtci install-capk
./kubevirtci create-cluster

## kubectl -n kvcluster wait --for=condition=Ready vmi/myjob