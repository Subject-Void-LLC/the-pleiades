#!/bin/sh
# Starts sshd, ovsdb-server and ovs-vswitchd, and opens the database socket to the ovs group.
# No bridge is made here: the runbook switch.yaml makes br0 and its ports through Pleiades.
set -e
ssh-keygen -A >/dev/null
/usr/sbin/sshd
mkdir -p /var/run/openvswitch /etc/openvswitch /var/log/openvswitch
[ -f /etc/openvswitch/conf.db ] || ovsdb-tool create /etc/openvswitch/conf.db /usr/share/openvswitch/vswitch.ovsschema
ovsdb-server --remote=punix:/var/run/openvswitch/db.sock --pidfile --detach \
  --log-file=/var/log/openvswitch/ovsdb-server.log /etc/openvswitch/conf.db
ovs-vsctl --no-wait init
ovs-vswitchd --pidfile --detach --log-file=/var/log/openvswitch/ovs-vswitchd.log
chgrp ovs /var/run/openvswitch/db.sock
chmod 0770 /var/run/openvswitch/db.sock
exec tail -F /var/log/openvswitch/ovs-vswitchd.log
