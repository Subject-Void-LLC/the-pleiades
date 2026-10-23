#!/bin/sh
# Builds the lab's two images. DOCKER_CONFIG points at an empty config, since the
# desktop credential helper times out on this machine.
set -e
here=$(cd "$(dirname "$0")" && pwd)
# An empty Docker config here, when there is one, avoids a credential helper that times out.
[ -d "$here/../.dockercfg" ] && export DOCKER_CONFIG="$here/../.dockercfg"
docker build -t frrlab/frr-ssh:10.1.1 "$here/frr"
docker build -t frrlab/ovs-ssh:alpine3.20.10 "$here/ovs"
