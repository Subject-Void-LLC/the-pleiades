#!/bin/bash
# Starts nginx in the background, then execs sshd in the foreground so it
# becomes PID 1 and `docker logs` shows its output, matching the plain
# and wordpress lab images' own single-foreground-process convention.
set -euo pipefail

service nginx start

exec /usr/sbin/sshd -D -e
