#!/usr/bin/env bash
# Connects this machine to the DevNet CML sandbox's AnyConnect VPN with
# openconnect, so pleiades and the import-cml tests reach the controller
# and the lab's nodes. Settings come from tests/cml/.env (see
# .env.example), read as KEY=VALUE data and never sourced; the password
# goes to openconnect on its standard input, never on a command line.
#
#   tests/cml/vpn.sh up | down | status
#
# Where this machine cannot run openconnect as root, --container runs it in
# a container instead (Docker holds the privileges), and the CML tests run
# in containers on that one's network:
#
#   tests/cml/vpn.sh up --container | down --container | status --container
#   tests/cml/vpn.sh run <command...>    # on the VPN's network; the repo is /src
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
env_file="$here/.env"
pid_file=/run/pleiades-cml-vpn.pid
iface=plcml0
container=pleiades-cml-vpn
image=pleiades/cml-vpn:1

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

# The container mode: openconnect in a container holding its own tunnel.
container_up() {
	load
	if [ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null)" = true ]; then
		echo "vpn.sh: already up in container $container"
		return 0
	fi
	docker rm -f "$container" >/dev/null 2>&1 || true
	docker build -q -t "$image" "$here" >/dev/null
	# The env file is mounted read only, so the password reaches
	# openconnect's standard input inside the container and no command line.
	docker run -d --name "$container" --cap-add NET_ADMIN --device /dev/net/tun \
		-v "$env_file:/run/cml.env:ro" "$image" >/dev/null
	for _ in $(seq 1 30); do
		docker exec "$container" ip link show "$iface" >/dev/null 2>&1 && break
		[ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null)" = true ] || break
		sleep 1
	done
	container_status
}

container_down() {
	docker rm -f "$container" >/dev/null 2>&1 && echo "vpn.sh: down" || echo "vpn.sh: not up"
}

container_status() {
	load
	if [ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null)" != true ]; then
		echo "vpn.sh: container $container is not running; its last words:"
		docker logs --tail 15 "$container" 2>&1 | sed 's/^/  /' || true
		return 1
	fi
	if docker exec "$container" ip -brief address show "$iface" 2>/dev/null; then
		echo "vpn.sh: tunnel $iface is up in $container; routes through it:"
		docker exec "$container" ip route show dev "$iface" | sed 's/^/  /'
	else
		echo "vpn.sh: tunnel $iface is not up yet in $container"
	fi
	if [ -n "$cml_url" ]; then
		# As status above: no credential is sent and nothing is trusted.
		local code
		code="$(docker exec "$container" curl -sk --connect-timeout 5 -o /dev/null -w '%{http_code}' "$cml_url/api/v0/system_information" || true)"
		echo "vpn.sh: $cml_url/api/v0/system_information answered ${code:-nothing}"
	fi
}

# run runs a command in a throwaway container on the VPN container's
# network, with the repository at /src.
run_on_vpn() {
	[ "$#" -gt 0 ] || die "usage: tests/cml/vpn.sh run <command...>"
	local repo
	repo="$(cd "$here/../.." && pwd)"
	# The controller's settings reach the command by name (-e NAME, the value
	# from this process's environment, never a command line); the VPN's
	# own login does not reach it at all.
	load
	local key pass=()
	for key in PLEIADES_CML_URL PLEIADES_CML_USERNAME PLEIADES_CML_PASSWORD PLEIADES_CML_TLS_FINGERPRINT; do
		export "$key=$(setting "$key" || true)"
		pass+=(-e "$key")
	done
	docker run --rm --network "container:$container" -v "$repo:/src" -w /src \
		-e HOME=/tmp "${pass[@]}" --user "$(id -u):$(id -g)" --entrypoint "$1" "$image" "${@:2}"
}

mode="${2:-}"
case "${1:-}:$mode" in
up:) up ;;
down:) down ;;
status:) status ;;
up:--container) container_up ;;
down:--container) container_down ;;
status:--container) container_status ;;
run:*) shift; run_on_vpn "$@" ;;
*) die "usage: tests/cml/vpn.sh up|down|status [--container], or run <command...>" ;;
esac
