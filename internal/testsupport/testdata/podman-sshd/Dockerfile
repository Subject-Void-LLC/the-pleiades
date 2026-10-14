# A real Podman behind a real OpenSSH server, for the tests that reach a
# Podman host the way a container host is reached: an SSH login, then
# Podman's own dial-stdio, through a service the session starts (Phase
# 119b). Built by testsupport.StartPodmanHost from the pinned base it
# passes as BASE.
#
# root runs Podman rootful; podman, the image's own rootless user, runs it
# rootless. The passwords are throwaway values for a container started
# fresh for each test, never a real device.
#
# UsePAM no: sshd checks the password against /etc/shadow itself. Through
# Fedora's PAM stack, every password was refused on the CI runners (Docker
# Engine on Ubuntu 24.04) and accepted under Docker Desktop, with the same
# image; the fixture tests the platform's SSH path, not the host's PAM.
ARG BASE
FROM ${BASE}
RUN dnf install -y --setopt=install_weak_deps=False openssh-server procps-ng \
 && dnf clean all \
 && ssh-keygen -A \
 && echo 'root:testpass123' | chpasswd \
 && echo 'podman:testpass123' | chpasswd \
 && mkdir -p /etc/ssh/sshd_config.d \
 && printf 'Port 2222\nPasswordAuthentication yes\nPermitRootLogin yes\nUsePAM no\n' > /etc/ssh/sshd_config.d/00-pleiades.conf
ENTRYPOINT ["/usr/sbin/sshd", "-D", "-e"]
