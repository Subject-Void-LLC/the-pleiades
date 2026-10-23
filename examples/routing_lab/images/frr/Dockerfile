# FRR 10.1.1 with an SSH server and an unprivileged login, netops, that may use
# vtysh (the frrvty group). Pleiades connects as netops; nothing logs in as root.
FROM quay.io/frrouting/frr:10.1.1
RUN apk add --no-cache openssh-server iputils-tracepath \
 && adduser -D -u 1000 -s /bin/sh netops \
 && echo 'netops:*' | chpasswd -e \
 && addgroup netops frrvty \
 && install -d -m 0700 -o netops -g netops /home/netops/.ssh \
 && install -d -m 0775 -o frr -g frrvty /var/log/frr \
 && printf 'service integrated-vtysh-config\n' > /etc/frr/vtysh.conf \
 && chown frr:frrvty /etc/frr/vtysh.conf && chmod 0644 /etc/frr/vtysh.conf \
 && sed -i -e 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' \
           -e 's/^#\?PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config
COPY start.sh /usr/local/bin/start.sh
RUN chmod 0755 /usr/local/bin/start.sh
CMD ["/usr/local/bin/start.sh"]
