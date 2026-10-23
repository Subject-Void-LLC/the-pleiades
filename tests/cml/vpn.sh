#!/usr/bin/env bash
# Connects this machine to the DevNet CML sandbox's AnyConnect VPN with
# openconnect, so pleiades and the import-cml tests reach the controller
# and the lab's nodes. Settings come from tests/cml/.env (see
# .env.example), read as KEY=VALUE data and never sourced; the password
# goes to openconnect on its standard input, never on a command line.
#
#   tests/cml/vpn.sh up | down | status
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
env_file="$here/.env"
pid_file=/run/pleiades-cml-vpn.pid
iface=plcml0

die() { echo "vpn.sh: $*" >&2; exit 1; }

# setting KEY prints KEY's value from the env file.
setting() {
	local key="$1" line
	while IFS= read -r line || [ -n "$line" ]; do
		case "$line" in
		"$key="*) printf '%s' "${line#*=}"; return 0 ;;
		esac
	done <"$env_file"
	return 1
}

load() {
	[ -f "$env_file" ] || die "no $env_file: copy .env.example beside it and fill it in"
	local mode
	mode="$(stat -c %a "$env_file")"
	[ "$mode" = 600 ] || die "$env_file is mode $mode and holds passwords: chmod 600 it"
	vpn_host="$(setting PLEIADES_CML_VPN_HOST)" || true
	vpn_user="$(setting PLEIADES_CML_VPN_USER)" || true
	cml_url="$(setting PLEIADES_CML_URL)" || true
	[ -n "$vpn_host" ] && [ -n "$vpn_user" ] || die "PLEIADES_CML_VPN_HOST and PLEIADES_CML_VPN_USER must be set in $env_file"
}

up() {
	load
	command -v openconnect >/dev/null || die "openconnect is not installed: sudo apt-get install -y openconnect"
	if sudo test -f "$pid_file" && sudo kill -0 "$(sudo cat "$pid_file")" 2>/dev/null; then
		echo "vpn.sh: already up ($iface)"
		return 0
	fi
	local password
	password="$(setting PLEIADES_CML_VPN_PASSWORD)" || die "PLEIADES_CML_VPN_PASSWORD must be set in $env_file"
	# sudo asks for its own password on the terminal first, so the VPN
	# password on standard input reaches openconnect alone.
	sudo -v
	printf '%s\n' "$password" | sudo openconnect --protocol=anyconnect --user="$vpn_user" --passwd-on-stdin \
		--non-inter --background --pid-file="$pid_file" --interface="$iface" "$vpn_host"
	unset password
	for _ in $(seq 1 20); do
		ip link show "$iface" >/dev/null 2>&1 && break
		sleep 1
	done
	status
}

down() {
	if sudo test -f "$pid_file"; then
		sudo kill "$(sudo cat "$pid_file")" 2>/dev/null || true
		sudo rm -f "$pid_file"
		echo "vpn.sh: down"
	else
		echo "vpn.sh: not up"
	fi
}

status() {
	load
	if ip -brief address show "$iface" 2>/dev/null; then
		echo "vpn.sh: tunnel $iface is up; routes through it:"
		ip route show dev "$iface" | sed 's/^/  /'
	else
		echo "vpn.sh: tunnel $iface is down"
	fi
	if [ -n "$cml_url" ]; then
		# No credential is sent, and the controller's certificate is not
		# checked here: this only asks whether anything answers. The import
		# itself verifies the certificate.
		local code
		code="$(curl -sk --connect-timeout 5 -o /dev/null -w '%{http_code}' "$cml_url/api/v0/system_information" || true)"
		echo "vpn.sh: $cml_url/api/v0/system_information answered ${code:-nothing}"
	fi
}

case "${1:-}" in
up) up ;;
down) down ;;
status) status ;;
*) die "usage: tests/cml/vpn.sh up|down|status" ;;
esac
