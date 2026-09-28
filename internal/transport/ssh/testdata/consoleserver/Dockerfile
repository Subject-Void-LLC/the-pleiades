# Console-server emulator for Phase 73's bastion proof: ser2net speaking
# RFC 2217 and raw TCP passthrough, backed by a real socat PTY pair.
#
# Built from Debian's own official base image rather than an unverified
# community ser2net image (the plan's own "pin the image and confirm its
# license" instruction: community images are not official). The only
# license surface to confirm is ser2net's own -- GPL-2 with OpenSSL
# exception, confirmed by reading /usr/share/doc/ser2net/copyright
# inside a real build, the standard Debian bookworm package (4.3.11-1)
# -- and socat's (GPL-2), both external daemons run in this container,
# never imported into The Pleiades' own binary, so the module's
# dependency-license rule does not reach them; confirmed anyway.
#
# ser2net.yaml's own "telnet(rfc2217),tcp,<port>" accepter syntax and
# the YAML config format itself were verified against a real container
# build before this file was written (ser2net switched config formats
# across major versions; guessing from memory would have been exactly
# the kind of unverified claim AGENTS.md's RULE 0 forbids).
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ser2net socat && \
    rm -rf /var/lib/apt/lists/*

COPY ser2net.yaml /etc/ser2net.yaml
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Deliberately NO `EXPOSE 7000`, and it must not come back. ser2net does
# listen on 7000; the point is that this image must not ASK for it to be
# published, because testcontainers-go publishes a port this container
# never wanted:
#
# When a ContainerRequest declares no ExposedPorts of its own,
# testcontainers-go inspects the image and exposes -- and host-publishes,
# on an ephemeral port -- every port the image's own EXPOSE names
# (lifecycle.go's "Expose ports automatically if the container request
# exposes zero ports"). So asking for nothing produced a host binding,
# which is the exact opposite of what ssh_bastion_proof_test.go's
# topology needs, and it went unnoticed because that test only ever
# dialed the container's own address.
#
# It is not merely cosmetic. A published port installs an ACCEPT rule in
# the daemon's DOCKER chain that matches traffic arriving from ANY other
# bridge network, not just from the host, so it punches straight through
# the inter-network isolation the bastion proof exists to demonstrate --
# measured directly, not inferred: with two networks and two otherwise
# identical containers, the published one was reachable across networks
# and the unpublished one was not.
ENTRYPOINT ["/entrypoint.sh"]
