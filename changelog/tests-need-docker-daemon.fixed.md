Tests that build an image or ask docker compose now need a Docker daemon that answers `docker info`, and the compose
test a working `docker compose` plugin, rather than only a `docker` command on the PATH. On a machine with just the
docker CLI (a Mac using Podman) they skip as `needs docker` instead of failing; CI's fast job requires docker, so a
runner that loses its daemon still fails.
