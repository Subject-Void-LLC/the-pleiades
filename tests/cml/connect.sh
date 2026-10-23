#!/bin/sh
# Reads the VPN settings from the env file mounted at /run/cml.env, as
# KEY=VALUE data, and runs openconnect in the foreground with the password
# on its standard input.
set -eu
setting() { sed -n "s/^$1=//p" /run/cml.env | head -n 1; }
host="$(setting PLEIADES_CML_VPN_HOST)"
user="$(setting PLEIADES_CML_VPN_USER)"
[ -n "$host" ] && [ -n "$user" ] || { echo "connect.sh: PLEIADES_CML_VPN_HOST and PLEIADES_CML_VPN_USER must be set" >&2; exit 1; }
setting PLEIADES_CML_VPN_PASSWORD | exec openconnect --protocol=anyconnect --user="$user" --passwd-on-stdin --non-inter --interface=plcml0 "$host"
