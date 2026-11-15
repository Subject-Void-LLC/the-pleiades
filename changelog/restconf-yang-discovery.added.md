A RESTCONF device can be onboarded and read: `generic_restconf` finds its root from `/.well-known/host-meta`, read with
no credential (or the conventional `/restconf` when it answers 404 or a redirect, as IOS XE answers anything but a valid login with a
302 to its WebUI's login page), with its
own TLS settings (verified over https) and stored credential, and `net.restconf.facts` records its capabilities, datastores and YANG modules as the
`restconf.*` facts and the same `yang.*` fact names a NETCONF session records; each protocol's library lists its own
modules, so one device's module set and digest differ between the two.
