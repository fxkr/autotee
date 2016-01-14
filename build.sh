#!/bin/bash

set -eu

# Go to where this script is
cd "$( dirname "${BASH_SOURCE[0]}" )"
HERE="$(pwd)"

# If the working directory is clean, set autotee.Version.
FLAGS=()
if git diff-files --quiet ; then
    VERSION="$(git describe --always)"
    FLAGS+=("-ldflags" "-X autotee.Version=${VERSION}")
fi

# If GOPATH is unset, set it to the current directory.
if [ -z ${GOPATH+x} ]; then
    export GOPATH="$(pwd)"
fi

go get autotee
go build "${FLAGS[@]:+${FLAGS[@]}}" src/autotee.go

