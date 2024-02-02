#!/bin/bash

set -ex

make cluster-up
make cluster-sync-split
make e2e-test
