#!/bin/sh
set -e

# Starts a real PTY pair: /tmp/ttyA is what ser2net.yaml's own
# connector opens, /tmp/ttyB is the "far side" a test can open directly
# to observe or inject bytes independent of the RFC 2217 session.
socat -d -d pty,link=/tmp/ttyA,raw,echo=0 pty,link=/tmp/ttyB,raw,echo=0 &

# Wait for socat to actually create both links before ser2net tries to
# open one; a fixed sleep would be neither deterministic nor an honest
# readiness check.
i=0
while [ ! -e /tmp/ttyA ] || [ ! -e /tmp/ttyB ]; do
  i=$((i + 1))
  if [ "$i" -gt 50 ]; then
    echo "socat never created both PTY links" >&2
    exit 1
  fi
  sleep 0.1
done

# -n: do not daemonize, so this stays PID 1 and the container's own
# lifecycle matches ser2net's.
exec ser2net -c /etc/ser2net.yaml -d -n
