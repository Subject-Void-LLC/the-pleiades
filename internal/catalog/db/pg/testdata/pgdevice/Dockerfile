# A database host as a db.pg device: the PostgreSQL image's own server and
# client programs, with an SSH server and an unprivileged login, reached the
# way a real device is, over SSH. Its pg_dump and pg_restore load the libpq
# built from the same release (/usr/local/lib), unlike a distribution's
# client package, which may load a newer libpq5, so the client's major
# version and libpq's are the same here. The base arrives as a build
# argument from testsupport.PostgresImage, the one pin tools/testimages
# reads.
ARG BASE
FROM ${BASE}

# The login's password is a throwaway test value in a throwaway container.
RUN apk add --no-cache openssh-server \
 && adduser -D -s /bin/sh device \
 && echo 'device:device-pw-for-tests' | chpasswd \
 && mkdir -p /etc/ssh/sshd_config.d \
 && printf 'PasswordAuthentication yes\nAllowTcpForwarding yes\nAllowStreamLocalForwarding yes\nAllowUsers device\n' > /etc/ssh/sshd_config.d/device.conf \
 && ssh-keygen -A

COPY device-entrypoint.sh /usr/local/bin/device-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/device-entrypoint.sh"]
CMD ["postgres"]
