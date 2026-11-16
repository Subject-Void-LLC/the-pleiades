#!/bin/sh
# Writes the configuration the test passed in SNMPD_CONF (which names the
# listening address, agentAddress udp:1161), then runs snmpd in the
# foreground, logging to standard output, as the unprivileged account
# the image runs as. Its persistent store (the engine ID, boots and every
# value a SET wrote) lives in a directory that account owns.
set -e
printf '%s\n' "$SNMPD_CONF" > /var/lib/agent/snmpd.conf
export SNMP_PERSISTENT_DIR=/var/lib/agent/persist
mkdir -p "$SNMP_PERSISTENT_DIR"
exec snmpd -f -Lo -C -c /var/lib/agent/snmpd.conf
