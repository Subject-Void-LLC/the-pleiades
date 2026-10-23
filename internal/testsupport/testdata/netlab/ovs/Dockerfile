# Open vSwitch on its userspace (netdev) datapath, with an SSH server and an
# unprivileged operator account allowed to use the database, as a test
# switch. The base image arrives from testsupport.OVSBaseImage.
ARG BASE
FROM ${BASE}

RUN apk add --no-cache openvswitch openssh-server tini \
 && adduser -D -s /bin/sh netops \
 && (passwd -u netops 2>/dev/null || true) \
 && mkdir -p /home/netops/.ssh && chown netops /home/netops/.ssh && chmod 0700 /home/netops/.ssh \
 && printf 'PasswordAuthentication no\nPermitRootLogin no\nAllowUsers netops\n' > /etc/ssh/sshd_config.d/lab.conf

COPY start.sh /usr/local/bin/start.sh
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/start.sh"]
