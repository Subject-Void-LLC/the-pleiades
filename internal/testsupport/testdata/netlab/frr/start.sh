#!/bin/sh
# Writes the test's public key for netops, starts sshd and FRR, and makes
# /var/run/frr writable to the frr group, where frr-reload.py writes as
# netops.
set -e
printf '%s\n' "$NETOPS_AUTHORIZED_KEY" > /home/netops/.ssh/authorized_keys
chown netops /home/netops/.ssh/authorized_keys
chmod 0600 /home/netops/.ssh/authorized_keys
ssh-keygen -A >/dev/null
/usr/sbin/sshd
/usr/lib/frr/docker-start &
for _ in $(seq 1 30); do
  vtysh -c 'show version' >/dev/null 2>&1 && break
  sleep 1
done
chmod g+w /var/run/frr
echo "netlab router ready"
wait
