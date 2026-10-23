# Open vSwitch on Alpine 3.20.10, userspace datapath, with an SSH server and an
# unprivileged login, netops, allowed to use the OVS database (the ovs group).
FROM alpine:3.20.10
RUN apk add --no-cache openvswitch openssh-server iproute2 \
 && addgroup -S ovs \
 && adduser -D -u 1000 -s /bin/sh netops \
 && echo 'netops:*' | chpasswd -e \
 && addgroup netops ovs \
 && install -d -m 0700 -o netops -g netops /home/netops/.ssh \
 && sed -i -e 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' \
           -e 's/^#\?PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config
COPY start.sh /usr/local/bin/start.sh
RUN chmod 0755 /usr/local/bin/start.sh
CMD ["/usr/local/bin/start.sh"]
