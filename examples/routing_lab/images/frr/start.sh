#!/bin/sh
# Starts sshd, then FRR's own docker-start (watchfrr and the daemons /etc/frr/daemons enables).
# /var/run/frr is made writable by the frrvty group: frr-reload.py, which net.frr.config runs
# as the login account, writes its reload files there, and vtysh access alone is not enough.
set -e
ssh-keygen -A >/dev/null
chgrp frrvty /var/run/frr
chmod 2775 /var/run/frr
/usr/sbin/sshd
exec /usr/lib/frr/docker-start
