#!/bin/sh
# Start sshd in the background, then the engine in the foreground through
# the base image's own entrypoint. A test may drop a file into
# /etc/ssh/sshd_config.d before start; the Include at the top of
# sshd_config means its settings win, since sshd takes the first value of
# each keyword.
set -e
/usr/sbin/sshd -e
# DOCKERD_EXTRA_ARGS lets a test add a listener (a TLS one); it is split on
# spaces deliberately, and set only by tests.
# shellcheck disable=SC2086
exec dockerd-entrypoint.sh dockerd --host=unix:///var/run/docker.sock --group=docker ${DOCKERD_EXTRA_ARGS:-}
