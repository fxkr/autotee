#!/bin/bash

set -eu

# Find out where this script is
HERE="$( cd "$( dirname "${BASH_SOURCE[0]}" )" ;  pwd)"

# If the working directory is clean, set the Version string.
FLAGS=()
if git --git-dir="$HERE/.git" --work-tree="$HERE" diff-files --quiet ; then
    VERSION="$(git --git-dir="$HERE/.git" --work-tree="$HERE" describe --always)"
    FLAGS+=("-ldflags" "-X github.com/fxkr/autotee/src.Version=${VERSION}")
fi

go build "${FLAGS[@]:+${FLAGS[@]}}" "$HERE/autotee.go"

