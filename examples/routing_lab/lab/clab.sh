#!/bin/sh
# Runs containerlab from its own image (native containerlab fails on Docker Desktop).
# Usage: lab/clab.sh deploy|destroy|inspect
set -e
here=$(cd "$(dirname "$0")" && pwd)
cd "$here"
# An empty Docker config here, when there is one, avoids a credential helper that times out.
[ -d "$here/../.dockercfg" ] && export DOCKER_CONFIG="$here/../.dockercfg"
extra=""
[ "$1" = destroy ] && extra="--cleanup"
exec docker run --rm --privileged --network host --pid host \
  -v /var/run/docker.sock:/var/run/docker.sock -v /var/run/netns:/var/run/netns \
  -v "$here/..":"$here/.." -w "$here" \
  ghcr.io/srl-labs/clab:0.74.3 containerlab "$1" -t frrlab.clab.yml $extra
