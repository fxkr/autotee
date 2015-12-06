#!/bin/bash

set -eu

# Go to where this script is
cd "$( dirname "${BASH_SOURCE[0]}" )"
HERE="$(pwd)"

# If the working directory is clean, set rtmptee.Version.
FLAGS=()
if git diff-files --quiet ; then
    VERSION="$(git describe --always)"
    FLAGS+=("-ldflags" "-X rtmptee.Version=${VERSION}")
fi

# If GOPATH is unset, set it to the current directory.
if [ -z ${GOPATH+x} ]; then
    export GOPATH="$(pwd)"
fi

go get rtmptee
go build "${FLAGS[@]:+${FLAGS[@]}}" src/rtmptee.go

