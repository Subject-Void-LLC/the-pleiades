# The DevNet CML sandbox's VPN in a container, for a machine where
# openconnect cannot run as root: tests/cml/vpn.sh up --container starts it,
# and vpn.sh run runs a command on its network.
FROM debian:bookworm-20261005-slim
RUN apt-get update && apt-get install -y --no-install-recommends openconnect ca-certificates curl iproute2 openssh-client \
 && rm -rf /var/lib/apt/lists/*
COPY connect.sh /usr/local/bin/connect.sh
ENTRYPOINT ["/usr/local/bin/connect.sh"]
