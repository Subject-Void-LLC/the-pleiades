Added 25 network and addressing filters, callable from `when`, `when_or`, and
`when_cel`: CIDR/netmask/wildcard-mask conversion, subnet splitting and
summarization, IP-to-integer conversion, IPv4-mapped IPv6 conversion, IP
classification (private/public/loopback/link-local/multicast), MAC address
normalization (Cisco, colon, and Windows notation) and OUI extraction, VLAN
and ASN validation, Cisco IOS interface name normalization, and FQDN/hostname
and URL domain/port extraction. See the generated filter reference for the
full list.

Added `pleiades forge new-filter`, a fourth Forge scaffolder that generates a
new `pkg/filters` function, its starter test, and a paste-ready CEL
registration block for extending the filter library.
