#!/bin/sh
# Writes the test's public key for netops, starts sshd and FRR, and makes
# /var/run/frr writable to the frr group, where frr-reload.py writes as
# netops.
#
# It says ready only once a configuration session can be opened. `show
# version` answers inside vtysh before any daemon is up, and FRR 10's mgmtd
# locks the running datastore while watchfrr reads the startup configuration
# and while each backend daemon syncs, so a test's first `configure terminal`
# raced both and was refused with "could not lock running DS" under load.
set -e
printf '%s\n' "$NETOPS_AUTHORIZED_KEY" > /home/netops/.ssh/authorized_keys
chown netops /home/netops/.ssh/authorized_keys
chmod 0600 /home/netops/.ssh/authorized_keys
ssh-keygen -A >/dev/null
/usr/sbin/sshd
/usr/lib/frr/docker-start &

# settled reports whether watchfrr has finished starting every daemon and
# reading the configuration, mgmtd's backends have connected, and a
# configuration session takes the lock.
settled() {
  w=$(vtysh -c 'show watchfrr' 2>/dev/null) || return 1
  printf '%s\n' "$w" | grep -q 'global phase: Idle' || return 1
  printf '%s\n' "$w" | grep -q 'Reading Configuration: no' || return 1
  # Every daemon line after the header, and none of them anything but Up.
  printf '%s\n' "$w" | sed -n '/Reading Configuration/,$p' | sed 1d | grep -q . || return 1
  if printf '%s\n' "$w" | sed -n '/Reading Configuration/,$p' | sed 1d | grep -v ' Up$' | grep -q .; then
    return 1
  fi
  b=$(vtysh -c 'show mgmt backend-adapter all' 2>/dev/null) || return 1
  printf '%s\n' "$b" | grep -q 'Client:[[:space:]]*zebra' || return 1
  printf '%s\n' "$b" | grep -q 'Client:[[:space:]]*staticd' || return 1
  vtysh -c 'configure terminal' -c 'end' >/dev/null 2>&1
}

ok=
for _ in $(seq 1 180); do
  if settled; then
    ok=1
    break
  fi
  sleep 0.5
done
if [ -z "$ok" ]; then
  echo "netlab router never accepted a configuration session" >&2
  vtysh -c 'show watchfrr' >&2 || true
  exit 1
fi
chmod g+w /var/run/frr
echo "netlab router ready"
wait
