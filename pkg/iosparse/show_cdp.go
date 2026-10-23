// Ported from genieparser's src/genie/libs/parser/iosxe/show_cdp.py (commit
// e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its affiliates,
// Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowCdpNeighbors: genieparser iosxe/show_cdp.py's ShowCdpNeighbors.cli,
// for "show cdp neighbors".
package iosparse

import "strconv"

// ShowCdpNeighbors' expressions, named as genieparser names them.
var (
	cdpP1 = re(`^(?P<device_id>\S+) +` + `(?P<local_interface>[a-zA-Z]+[\s]*[\d\/\.]+) +` + `(?P<hold_time>\d+) +(?P<capability>[RTBSsHIrPDCM\s]+)( +` + `(?P<platform>\S+))?` + `(\s+(?P<port_id>(Fa|Gi|GE).\s*\d*\/*\d*))?$`)
	cdpP2 = re(`^(?P<device_id>\S+) +` + `(?P<local_interface>[a-zA-Z]+[\s]*[\d\/\.]+) +` + `(?P<hold_time>\d+) +` + `(?P<capability>[RTBSsHIrPDCM\s]+)` + `(?: +(?P<platform>[\w\-]+ (\d+)?))?( +` + `(?P<port_id>[a-zA-Z0-9\/]+( [a-zA-Z0-9\/\s]+)?))?$`)
	cdpP3 = re(`^(?P<device_id>\S+) +` + `(?P<local_interface>[a-zA-Z]+[\s]*[\d\/\.]+) +` + `(?P<hold_time>\d+) +(?P<capability>[RTBSsHIrPDCM\s]+)( +` + `(?P<platform>\S+(?: \d+)?))?( ` + `(?P<port_id>[\s\S]+))?$`)
	cdpP4 = re(`^(?P<device_id>\S+)$`)
	cdpP5 = re(`(?P<local_interface>[a-zA-Z]+[\s]*[\d/.]+(:\d+)?)?\s+` + `(?P<hold_time>\d+) +(?P<capability>[RTBSsHIrPDCM\s]+)( +` + `(?P<platform>VMware ES|\S+))?( (?P<port_id>[\.a-zA-Z0-9/\s]+(:\d+)?))?$`)
	cdpP6 = re(`^Total cdp entries displayed :\s+(?P<total_entries>\d+)$`)
)

// cdpState is ShowCdpNeighbors.cli's local variables.
type cdpState struct {
	// parsed is parsed_dict.
	parsed *dict
	// index numbers the neighbors.
	index int64
	// device is device_dict, nil until a neighbor has been read.
	device *dict
}

// parseShowCdpNeighbors is ShowCdpNeighbors.cli: each neighbor by its
// position in the table, and the total.
func parseShowCdpNeighbors(out string, _ map[string]string) *dict {
	s := &cdpState{parsed: newDict()}
	for _, raw := range splitlines(out) {
		s.line(strip(raw))
	}
	return s.parsed
}

// entry is parsed_dict['cdp']['index'][index], created when missing.
func (s *cdpState) entry() *dict {
	s.device = s.parsed.sub("cdp").sub("index").sub(strconv.FormatInt(s.index, 10))
	return s.device
}

// stripGroup is m.groupdict()[name].strip(), raising AttributeError for a
// group that took no part, as None has no strip.
func stripGroup(m *mo, name string) string {
	if !m.has(name) {
		raise("AttributeError", "'NoneType' object has no attribute 'strip'")
	}
	return strip(m.g(name))
}

// line handles one stripped line, in genieparser's order.
func (s *cdpState) line(line string) {
	m := cdpP1.match(line)
	if m == nil {
		m = cdpP2.match(line)
	}
	if m == nil {
		m = cdpP3.match(line)
	}
	if m != nil {
		s.index++
		d := s.entry()
		d.set("device_id", strip(m.g("device_id")))
		d.set("local_interface", convertIntfName(strip(m.g("local_interface"))))
		d.set("hold_time", pyInt(m.g("hold_time")))
		d.set("capability", strip(m.g("capability")))
		d.set("platform", strip(m.g("platform")))
		d.set("port_id", convertIntfName(stripGroup(m, "port_id")))
		return
	}
	if m := cdpP4.match(line); m != nil {
		if !contains(m.g("device_id"), "Eth") {
			s.index++
			s.entry().set("device_id", strip(m.g("device_id")))
		} else {
			s.dev().set("port_id", convertIntfName(strip(m.g("device_id"))))
		}
		return
	}
	if m := cdpP5.match(line); m != nil {
		d := s.entry()
		d.set("local_interface", convertIntfName(stripGroup(m, "local_interface")))
		d.set("hold_time", pyInt(m.g("hold_time")))
		d.set("capability", strip(m.g("capability")))
		if m.g("platform") != "" {
			d.set("platform", strip(m.g("platform")))
		}
		if m.g("port_id") != "" {
			d.set("port_id", convertIntfName(strip(m.g("port_id"))))
		}
		return
	}
	if m := cdpP6.match(line); m != nil {
		s.parsed.d("cdp").set("total_entries", pyInt(m.g("total_entries")))
	}
}

// dev is device_dict, NameError before any neighbor.
func (s *cdpState) dev() *dict {
	if s.device == nil {
		raise("NameError", "device_dict")
	}
	return s.device
}
