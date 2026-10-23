# A real Telnet server with a login, for the Telnet back end's tests:
# BusyBox's telnetd running BusyBox's /bin/login, an account netops whose
# password the test passes in, and a shell prompt "lab$ ".
ARG BASE
FROM ${BASE}
RUN apk add --no-cache busybox-extras \
 && adduser -D -s /bin/sh netops \
 && printf 'export PS1="lab$ "\n' > /etc/profile.d/prompt.sh
COPY start.sh /usr/local/bin/start.sh
EXPOSE 23
ENTRYPOINT ["/bin/sh", "/usr/local/bin/start.sh"]
