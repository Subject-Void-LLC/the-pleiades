// Ported from genieparser's src/genie/libs/parser/iosxe/show_interface.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowIpInterfaceBrief: genieparser iosxe/show_interface.py's
// ShowIpInterfaceBrief.cli, for "show ip interface brief".
package iosparse

// briefTable is the table ShowIpInterfaceBrief reads.
var briefTable = tabular{
	headers:  []string{"Interface", "IP-Address", `OK\?`, "Method", "Status", "Protocol"},
	labels:   []string{"Interface", "ip_address", "interface_is_ok", "method", "status", "protocol"},
	terminal: re(`^\n`),
}

// parseShowIpInterfaceBrief is ShowIpInterfaceBrief.cli: each row of the
// table, keyed by the interface's full name.
func parseShowIpInterfaceBrief(out string, _ map[string]string) *dict {
	parsed := newDict()
	entries := briefTable.fill(out)
	for _, intf := range entries.keys {
		row := entries.d(intf)
		row.del("Interface")
		parsed.sub("interface").set(convertIntfName(intf), row)
	}
	return parsed
}
