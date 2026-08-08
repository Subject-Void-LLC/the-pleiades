# A standard "bootstrap a webserver" pattern: Ubuntu + nginx, installed
# and running, serving the stock default site. This is the shape most
# real-world "provision a webserver" runbooks target: install the
# package, enable the service, confirm it answers on port 80.
FROM ubuntu:24.04

RUN apt-get update && \
    apt-get install -y --no-install-recommends openssh-server sudo curl ca-certificates nginx && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /var/run/sshd

RUN useradd -m -s /bin/bash -G sudo svc-netauto && \
    echo 'svc-netauto:pleiades-lab-2026' | chpasswd && \
    echo 'svc-netauto ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/svc-netauto

COPY keys/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key
COPY keys/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ed25519_key.pub
RUN chmod 600 /etc/ssh/ssh_host_ed25519_key && \
    rm -f /etc/ssh/ssh_host_rsa_key* /etc/ssh/ssh_host_ecdsa_key* && \
    printf 'HostKey /etc/ssh/ssh_host_ed25519_key\nPasswordAuthentication yes\nPubkeyAuthentication yes\n' \
        > /etc/ssh/sshd_config.d/lab.conf

COPY start.sh /usr/local/bin/start.sh
RUN chmod 755 /usr/local/bin/start.sh

EXPOSE 22 80
CMD ["/usr/local/bin/start.sh"]
