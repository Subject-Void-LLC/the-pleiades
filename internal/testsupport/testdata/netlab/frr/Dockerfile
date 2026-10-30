# FRRouting with an SSH server and an unprivileged operator account, as a
# test router: reached the way a real one is, over SSH, never docker exec.
# The base image arrives as a build argument from testsupport.FRRBaseImage,
# the one pin tools/testimages reads.
ARG BASE
FROM ${BASE}

# sudo, with a rule letting the operator become root and frr without a
# password, for the tests of a task's become. Becoming anyone else is not
# allowed, which is the refusal those tests read.
RUN apk add --no-cache openssh-server iputils-tracepath sudo \
 && mkdir -p /etc/sudoers.d && printf 'netops ALL=(root,frr) NOPASSWD: ALL\n' > /etc/sudoers.d/netops && chmod 0440 /etc/sudoers.d/netops \
 && adduser -D -s /bin/sh -G frr netops \
 && addgroup netops frrvty \
 && (passwd -u netops 2>/dev/null || true) \
 && sed -i -e 's/^ospfd=no/ospfd=yes/' -e 's/^bgpd=no/bgpd=yes/' /etc/frr/daemons \
 && printf 'service integrated-vtysh-config\n' > /etc/frr/vtysh.conf \
 && touch /etc/frr/frr.conf \
 && chown -R frr:frr /etc/frr && chown netops:frr /etc/frr/frr.conf \
 && chmod 0775 /etc/frr && chmod 0664 /etc/frr/frr.conf \
 && mkdir -p /var/log/frr && chown frr:frr /var/log/frr && chmod 0775 /var/log/frr \
 && mkdir -p /home/netops/.ssh && chown netops /home/netops/.ssh && chmod 0700 /home/netops/.ssh \
 && printf 'PasswordAuthentication no\nPermitRootLogin no\nAllowUsers netops\n' > /etc/ssh/sshd_config.d/lab.conf

COPY start.sh /usr/local/bin/start.sh
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/start.sh"]
