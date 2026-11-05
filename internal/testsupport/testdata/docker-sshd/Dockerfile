# A real Docker engine behind a real OpenSSH server, for the tests that
# reach an engine the way a container host is reached: an SSH login, then
# a channel to the engine's socket (Phase 119a). Built by
# testsupport.StartDockerHost from the pinned base it passes as BASE, so
# the engine's version is the Go constant DockerHostBaseImage, which CI's
# image cache reads.
#
# testuser is in the docker group, as a host's automation account is;
# outsider is not, so a test can see the engine refuse a login that may
# not open its socket. The passwords are throwaway values for a container
# started fresh for each test, never a real device.
#
# The base ships OpenSSH's client packages at its own release, and Alpine's
# index moves on: openssh-server at the index's release cannot sit beside
# the base's openssh-client-common, so apk refused to install it (2026-11-05,
# 10.3_p1-r1 in the image against r2 in the index). --upgrade moves the
# client packages to the index's release with the server. The engine is not
# an apk package here, so its pinned version is unchanged.
ARG BASE
FROM ${BASE}
RUN apk add --no-cache --upgrade openssh-server openssh-client-common openssh-client-default \
 && ssh-keygen -A \
 && adduser -D -s /bin/sh testuser \
 && adduser -D -s /bin/sh outsider \
 && echo 'testuser:testpass123' | chpasswd \
 && echo 'outsider:testpass123' | chpasswd \
 && addgroup testuser docker \
 && mkdir -p /etc/ssh/sshd_config.d \
 && printf 'Include /etc/ssh/sshd_config.d/*.conf\n' > /etc/ssh/sshd_config.new \
 && cat /etc/ssh/sshd_config >> /etc/ssh/sshd_config.new \
 && mv /etc/ssh/sshd_config.new /etc/ssh/sshd_config \
 && printf 'Port 2222\nPasswordAuthentication yes\nKbdInteractiveAuthentication no\n' >> /etc/ssh/sshd_config
COPY entrypoint.sh /usr/local/bin/pleiades-docker-sshd
ENTRYPOINT ["/usr/local/bin/pleiades-docker-sshd"]
