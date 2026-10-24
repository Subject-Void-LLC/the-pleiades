# A GNS3 server for the lab importer's tests: gns3-server from PyPI at
# GNS3_VERSION, with VPCS (a PC whose telnet console answers show ip) and
# uBridge (which VPCS needs), each built from its own pinned release.
ARG BASE
FROM ${BASE} AS build
RUN apt-get update && apt-get install -y --no-install-recommends gcc make libc6-dev libpcap-dev curl ca-certificates && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://github.com/GNS3/vpcs/archive/refs/tags/v0.8.4.tar.gz | tar xz && cd vpcs-0.8.4/src && ./mk.sh && cp vpcs /usr/local/bin/vpcs
RUN curl -fsSL https://github.com/GNS3/ubridge/archive/refs/tags/v1.2.3.tar.gz | tar xz && cd ubridge-1.2.3 && make && cp ubridge /usr/local/bin/ubridge

FROM ${BASE}
ARG GNS3_VERSION
COPY --from=build /usr/local/bin/vpcs /usr/local/bin/ubridge /usr/local/bin/
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev util-linux libpcap0.8 \
 && pip install --no-cache-dir "gns3-server==${GNS3_VERSION}" \
 && apt-get purge -y gcc libc6-dev && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*
EXPOSE 3080
ENTRYPOINT ["gns3server", "--host", "0.0.0.0", "--port", "3080"]
