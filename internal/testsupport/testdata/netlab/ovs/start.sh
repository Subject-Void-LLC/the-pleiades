#!/bin/sh
# Writes the test's public key for netops, starts ovsdb-server and
# ovs-vswitchd, hands the database socket to netops, and runs sshd.
set -e
printf '%s\n' "$NETOPS_AUTHORIZED_KEY" > /home/netops/.ssh/authorized_keys
chown netops /home/netops/.ssh/authorized_keys
chmod 0600 /home/netops/.ssh/authorized_keys
ssh-keygen -A >/dev/null
mkdir -p /var/run/openvswitch /etc/openvswitch
[ -f /etc/openvswitch/conf.db ] || \
  ovsdb-tool create /etc/openvswitch/conf.db /usr/share/openvswitch/vswitch.ovsschema
ovsdb-server --remote=punix:/var/run/openvswitch/db.sock --pidfile --detach --log-file=/var/log/ovsdb-server.log
ovs-vsctl --no-wait init
ovs-vswitchd --pidfile --detach --log-file=/var/log/ovs-vswitchd.log
chown netops /var/run/openvswitch/db.sock
echo "netlab switch ready"
exec /usr/sbin/sshd -D
