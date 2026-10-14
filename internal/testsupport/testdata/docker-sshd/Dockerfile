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
ARG BASE
FROM ${BASE}
RUN apk add --no-cache openssh-server \
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
