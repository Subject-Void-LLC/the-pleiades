# A Debian device for the capability reach gate: OpenSSH as root with
# privilege separation, apt and dpkg, and the shadow tools (groupadd,
# useradd) Debian ships in every image. Root logs in directly, because
# nothing in The Pleiades elevates a task: a method that needs root runs as
# the account it logs in as. A throwaway test container, never a device.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server \
 && mkdir -p /run/sshd \
 && echo 'root:release-gate-debian-password' | chpasswd \
 && printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' > /etc/ssh/sshd_config.d/gate.conf
CMD ["/usr/sbin/sshd", "-D", "-e"]
