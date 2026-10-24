#!/bin/sh
# Sets netops' password from the test, then serves Telnet logins in the
# foreground.
set -e
printf 'netops:%s\n' "$NETOPS_PASSWORD" | chpasswd
echo "telnetd ready"
exec telnetd -F -p 23 -l /bin/login
