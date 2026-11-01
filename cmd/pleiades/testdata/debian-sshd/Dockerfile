# A Debian device for the capability reach, check and become gates: OpenSSH
# as root with privilege separation, apt and dpkg, the shadow tools
# (groupadd, useradd) Debian ships in every image, and sudo. Root logs in
# directly for the gates that need root. The deploy account logs in without
# root and has a NOPASSWD sudo rule, so the become gate can prove a task's
# commands really ran as root through sudo -n. A throwaway test container,
# never a device.
#
# jq and the libraries it pulls in are downloaded, not installed, and the
# slim image's docker-clean hook is removed so apt keeps them: a gate then
# installs jq through The Pleiades and rolls it back with no network at
# test time, as many times as it likes.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server sudo \
 && mkdir -p /run/sshd \
 && echo 'root:release-gate-debian-password' | chpasswd \
 && useradd -m -s /bin/sh deploy \
 && echo 'deploy:release-gate-deploy-password' | chpasswd \
 && echo 'deploy ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/deploy \
 && chmod 0440 /etc/sudoers.d/deploy \
 && printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' > /etc/ssh/sshd_config.d/gate.conf \
 && rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get install -y --download-only --no-install-recommends jq
CMD ["/usr/sbin/sshd", "-D", "-e"]
