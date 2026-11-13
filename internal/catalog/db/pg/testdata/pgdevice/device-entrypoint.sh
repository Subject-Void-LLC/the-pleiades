#!/bin/sh
# Starts the SSH server, then PostgreSQL through the image's own entrypoint.
# When /tls holds cert.pem and key.pem, the server serves TLS from them,
# installed as the server's own account requires of a key.
set -e
/usr/sbin/sshd -e
if [ -f /tls/key.pem ]; then
  install -o postgres -m 600 /tls/key.pem /tmp/server.key
  install -o postgres -m 644 /tls/cert.pem /tmp/server.crt
  set -- "$@" -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key
fi
exec docker-entrypoint.sh "$@"
