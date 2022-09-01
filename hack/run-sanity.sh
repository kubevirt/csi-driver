#!/bin/bash            
                                 
set -e -o pipefail

go test -v $PWD/sanity/...
