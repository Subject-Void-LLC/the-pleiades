# A real net-snmp agent for the SNMP client's tests: snmpd from Alpine's
# net-snmp package (5.9.4, built with Blumenthal's and Reeder's AES-192 and
# AES-256), run as an unprivileged account on UDP 1161, its configuration
# written by the test (testsupport.StartSNMPD). The base image arrives as a
# build argument from testsupport.SNMPDBaseImage, pinned by digest, the one
# pin tools/testimages reads.
ARG BASE
FROM ${BASE}
RUN apk add --no-cache net-snmp net-snmp-tools \
 && adduser -D -H -s /sbin/nologin agent \
 && mkdir -p /var/lib/agent && chown agent /var/lib/agent
COPY start.sh /usr/local/bin/start.sh
COPY dropset.sh /usr/local/bin/dropset.sh
USER agent
EXPOSE 1161/udp
ENTRYPOINT ["/bin/sh", "/usr/local/bin/start.sh"]
