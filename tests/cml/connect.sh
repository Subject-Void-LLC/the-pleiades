#!/bin/sh
# Reads the VPN settings from the env file mounted at /run/cml.env, as
# KEY=VALUE data, and runs openconnect in the foreground with the password
# on its standard input.
set -eu
setting() { sed -n "s/^$1=//p" /run/cml.env | head -n 1; }
host="$(setting PLEIADES_CML_VPN_HOST)"
user="$(setting PLEIADES_CML_VPN_USER)"
[ -n "$host" ] && [ -n "$user" ] || { echo "connect.sh: PLEIADES_CML_VPN_HOST and PLEIADES_CML_VPN_USER must be set" >&2; exit 1; }
pinned="$(setting PLEIADES_CML_VPN_SERVERCERT)"
# The sandbox's key changes between reservations and no CA here signs it, so
# accept the key the server presents now (as vpn.sh does): ask with a pin
# that matches no key, so openconnect names the server's own pin and stops
# before any credential is sent, then connect pinned to exactly that key.
bogus=pin-sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
pin="$(openconnect --protocol=anyconnect --authenticate --non-inter --servercert="$bogus" "$host" </dev/null 2>&1 \
	| grep -o 'pin-sha256:[A-Za-z0-9+/]*=*' | grep -v "^$bogus\$" | tail -n 1 || true)"
[ -n "$pin" ] || { echo "connect.sh: the server at $host named no certificate pin" >&2; exit 1; }
if [ -n "$pinned" ] && [ "$pinned" != "$pin" ]; then
	echo "connect.sh: WARNING: $host presents $pin, not the $pinned the env file pins; accepting it" >&2
else
	echo "connect.sh: accepting $host's certificate key $pin (not verified by any CA)" >&2
fi
setting PLEIADES_CML_VPN_PASSWORD | exec openconnect --protocol=anyconnect --user="$user" --passwd-on-stdin --non-inter --interface=plcml0 --servercert="$pin" "$host"
