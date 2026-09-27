# Baseline Ubuntu target: SSH only, nothing else installed. Matches the
# "linux_server" / "linux_server.debian_family" classification rule
# (internal/classification/default_ruleset.go) before any provisioning
# runbook has touched it. Used for add-host/add-credential/validate/run
# examples that do not need a real service running yet.
FROM ubuntu:24.04

RUN apt-get update && \
    apt-get install -y --no-install-recommends openssh-server sudo curl ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /var/run/sshd

# svc-netauto matches the credential name used in examples/upgrade_ios's
# own README, for a consistent voice across every example in this repo.
RUN useradd -m -s /bin/bash -G sudo svc-netauto && \
    echo 'svc-netauto:pleiades-lab-2026' | chpasswd && \
    echo 'svc-netauto ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/svc-netauto

# A fixed, checked-in host key (generated once for this lab, never
# rotated) so the container's identity is stable across rebuilds: a real
# device keeps the same host key for its lifetime, and The Pleiades' SSH
# transport fails closed on a key that does not match known_hosts
# (internal/transport/ssh/ssh.go), so a host key that changed on every
# `docker compose up` would break the exact trust model these examples
# are meant to demonstrate honestly.
COPY keys/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key
COPY keys/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ed25519_key.pub
RUN chmod 600 /etc/ssh/ssh_host_ed25519_key && \
    rm -f /etc/ssh/ssh_host_rsa_key* /etc/ssh/ssh_host_ecdsa_key* && \
    printf 'HostKey /etc/ssh/ssh_host_ed25519_key\nPasswordAuthentication yes\nPubkeyAuthentication yes\n' \
        > /etc/ssh/sshd_config.d/lab.conf

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
