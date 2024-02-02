#!/bin/bash

set -ex

make cluster-up
make cluster-sync
make e2e-test
