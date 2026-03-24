#!/usr/bin/env bash
# MeshPOP meshclaw build script
# This script requires the meshpoplinux source tree.
# Clone it: git clone https://github.com/meshpop/meshpoplinux ~/meshpoplinux
# Then set: export RTLINUX_DIR=~/meshpoplinux
set -e
OUTPUT_DIR="${1:-$(pwd)/output}"
echo "meshclaw build: RTLINUX_DIR=$RTLINUX_DIR OUTPUT_DIR=$OUTPUT_DIR"
echo "ERROR: Build scripts require meshpoplinux source tree."
echo "  git clone https://github.com/meshpop/meshpoplinux ~/meshpoplinux"
echo "  export RTLINUX_DIR=~/meshpoplinux"
exit 1
