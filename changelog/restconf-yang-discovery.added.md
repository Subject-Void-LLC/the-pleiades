A RESTCONF device can be onboarded and read: `generic_restconf` finds its root from `/.well-known/host-meta`, read with
no credential (or the conventional `/restconf` when it answers 404, as IOS XE does to anything but a valid login), with its
own TLS settings (verified over https) and stored credential, and `net.restconf.facts` records its capabilities, datastores and YANG modules as the
`restconf.*` facts and the same `yang.*` facts a NETCONF session records, so a device reads alike over either protocol.
