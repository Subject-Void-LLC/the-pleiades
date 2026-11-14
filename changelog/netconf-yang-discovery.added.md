Every NETCONF session discovers the device's capabilities, datastores and YANG modules (from its YANG library when it
has one) and records them as the `netconf.capabilities`, `netconf.datastores` and `yang.*` facts; `net.netconf.facts`
reads them without an edit, and a document naming a namespace the device does not serve is refused before it is sent.
