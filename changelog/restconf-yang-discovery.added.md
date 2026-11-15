A RESTCONF device can be onboarded and read: `generic_restconf` finds its root from `/.well-known/host-meta`, over its own
verified TLS and stored credential, and `net.restconf.facts` records its capabilities, datastores and YANG modules as the
`restconf.*` facts and the same `yang.*` facts a NETCONF session records, so a device reads alike over either protocol.
