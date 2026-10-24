// Ported from genieparser's src/genie/libs/parser/iosxe/show_ospf.py,
// src/genie/libs/parser/iosxe/show_lldp.py,
// src/genie/libs/parser/iosxe/show_archive.py (commit e62f9467), Copyright
// (c) 2021 Cisco Systems, Inc. and/or its affiliates, Apache License 2.0;
// changed: translated from Python to Go. See NOTICE.genieparser.

// The short parsers: genieparser's ShowIpOspfNeighbor (iosxe/show_ospf.py),
// ShowLldpNeighbors (iosxe/show_lldp.py) and ShowArchiveConfigDifferences
// (iosxe/show_archive.py).
package iosparse

import "strings"

// ShowIpOspfNeighbor's expression.
var ospfNbrP1 = re(`^(?P<neighbor>\S+) +(?P<pri>\d+) +(?P<state>\S+(?:\s+\S+)?)` + ` +(?P<dead_time>\S+) +(?P<address>\S+) +(?P<interface>\S+)$`)

// parseShowIpOspfNeighbor is ShowIpOspfNeighbor.cli: each neighbor, by
// interface.
func parseShowIpOspfNeighbor(out string, _ map[string]string) *dict {
	ret := newDict()
	for _, raw := range splitlines(out) {
		m := ospfNbrP1.match(strip(raw))
		if m == nil {
			continue
		}
		nbr := ret.sub("interfaces").sub(m.g("interface")).sub("neighbors").sub(m.g("neighbor"))
		nbr.set("priority", pyInt(m.g("pri")))
		nbr.set("state", m.g("state"))
		nbr.set("dead_time", m.g("dead_time"))
		nbr.set("address", m.g("address"))
	}
	return ret
}

// ShowLldpNeighbors' expressions.
var (
	lldpP1 = re(`^Total\s+entries\s+displayed:\s+(?P<entry>\d+)$`)
	lldpP2 = re(`(?P<device_id>.{20})(?P<interfaces>\S+)` + `\s+(?P<hold_time>\d+)\s+(?P<capabilities>[A-Z,]+)?` + `\s+(?P<port_id>\S+)`)
)

// parseShowLldpNeighbors is ShowLldpNeighbors.cli: each neighbor by local
// interface and remote port, and the total.
func parseShowLldpNeighbors(out string, _ map[string]string) *dict {
	parsed := newDict()
	for _, raw := range splitlines(out) {
		line := strip(raw)
		if m := lldpP1.match(line); m != nil {
			parsed.set("total_entries", pyInt(m.g("entry")))
			continue
		}
		m := lldpP2.match(line)
		if m == nil {
			continue
		}
		intf := convertIntfName(m.g("interfaces"))
		dev := parsed.sub("interfaces").sub(intf).sub("port_id").sub(m.g("port_id")).sub("neighbors").sub(m.g("device_id"))
		dev.set("hold_time", pyInt(m.g("hold_time")))
		if caps := m.g("capabilities"); caps != "" {
			var list []string
			for _, c := range strings.Split(caps, ",") {
				list = append(list, strip(c))
			}
			dev.set("capabilities", strList(list))
		}
	}
	return parsed
}

// ShowArchiveConfigDifferences' expressions. p3 to p6 are used only by
// the incremental-diffs subclass, which is not ported.
var (
	archP1 = re(`^\s*!Contextual +Config +Diffs:$`)
	archP2 = re(`^\s*(?P<line_info>(\+|\-)[\w\W]+)$`)
)

// parseShowArchiveConfigDifferences is ShowArchiveConfigDifferences.cli
// with no cmd argument: each "+" or "-" line after "!Contextual Config
// Diffs:", in order.
func parseShowArchiveConfigDifferences(out string, _ map[string]string) *dict {
	ret := newDict()
	var diff *list
	for _, raw := range splitlines(out) {
		line := strip(raw)
		if diff == nil {
			if archP1.match(line) != nil {
				diff = asList(ret.setdefault("diff", newList()))
			}
			continue
		}
		if m := archP2.match(line); m != nil {
			diff.appendItem(m.g("line_info"))
		}
	}
	return ret
}
