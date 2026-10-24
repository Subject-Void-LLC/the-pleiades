// Ported from genieparser's src/genie/libs/parser/iosxe/show_bgp.py,
// src/genie/libs/parser/iosxe/show_ip_bgp.py (commit e62f9467), Copyright
// (c) 2021 Cisco Systems, Inc. and/or its affiliates, Apache License 2.0;
// changed: translated from Python to Go. See NOTICE.genieparser.

// The BGP summary parsers: genieparser iosxe/show_bgp.py's
// ShowBgpSummarySuperParser.cli, called by ShowIpBgpSummary
// (iosxe/show_ip_bgp.py) and ShowBgpSummary (iosxe/show_bgp.py).
package iosparse

// ShowBgpSummarySuperParser's expressions, named as genieparser names them.
var (
	bgpP1   = re(`^For address family: +(?P<address_family>[a-zA-Z0-9\s\-\_]+)$`)
	bgpP2   = re(`^BGP +router +identifier` + ` +(?P<route_identifier>[0-9\.\:]+), +local +AS` + ` +number +(?P<local_as>[0-9\.]+)$`)
	bgpP3   = re(`^BGP +table +version +is` + ` +(?P<bgp_table_version>[0-9]+),` + ` +main +routing +table +version` + ` +(?P<routing_table_version>[0-9]+)$`)
	bgpP4   = re(`^(?P<networks>[0-9]+) +network +entries +using` + ` +(?P<bytes>[0-9]+) +bytes +of +memory$`)
	bgpP5   = re(`^(?P<path>[0-9]+) +path +entries +using` + ` +(?P<memory_usage>[0-9]+) +bytes +of +memory$`)
	bgpP5_1 = re(`^(?P<num_entries>([0-9]+)) +BGP` + ` +(?P<entries_type>(\S+)) +entries +using` + ` +(?P<entries_byte>[0-9]+) +bytes +of +memory$`)
	bgpP5_2 = re(`^(?P<num_community_entries>[0-9]+) +BGP +extended` + ` +community +entries +using` + ` +(?P<memory_usage>[0-9]+) +bytes +of +memory$`)
	bgpP6   = re(`^(?P<attribute_entries>(\S+)) +BGP` + ` +(?P<attribute_type>(\S+)) +attribute +entries` + ` +using +(?P<bytes>[0-9]+) +bytes +of +memory$`)
	bgpP6_1 = re(`^(?P<num_cache_entries>([0-9]+)) +BGP` + ` +(?P<cache_type>(\S+)) +cache +entries +using` + ` +(?P<cache_byte>[0-9]+) +bytes +of +memory$`)
	bgpP7   = re(`^BGP +using +(?P<total_memory>[0-9]+) +total +bytes` + ` +of +memory$`)
	bgpP8   = re(`^BGP +activity +(?P<activity_prefixes>(\S+))` + ` +prefixes, +(?P<activity_paths>(\S+)) +paths, +scan` + ` +interval +(?P<scan_interval>[0-9]+) +secs$`)
	bgpP9   = re(`^ *(?P<our_entry>\*)?(?P<neighbor>[a-zA-Z0-9\.\:]+) +(?P<version>[0-9]+)` + ` +(?P<as>[0-9]+(\.\d+)?) +(?P<msg_rcvd>[0-9]+)` + ` +(?P<msg_sent>[0-9]+) +(?P<tbl_ver>[0-9]+)` + ` +(?P<inq>[0-9]+) +(?P<outq>[0-9]+)` + ` +(?P<up_down>[a-zA-Z0-9\:]+)` + ` +(?P<state>[a-zA-Z0-9\(\)\s]+)$`)
	bgpP10  = re(`^(?P<our_entry>\*)?(?P<neighbor>[a-zA-Z0-9\.\:]+)$`)
	bgpP11  = re(`^(?P<version>[0-9]+)` + ` +(?P<as>[0-9]+(\.\d+)?) +(?P<msg_rcvd>[0-9]+)` + ` +(?P<msg_sent>[0-9]+) +(?P<tbl_ver>[0-9]+)` + ` +(?P<inq>[0-9]+) +(?P<outq>[0-9]+)` + ` +(?P<up_down>[a-zA-Z0-9\:]+)` + ` +(?P<state>[a-zA-Z0-9\(\)\s]+)$`)
)

// pyLocals holds Python local variables that may be read before they are
// assigned: reading an unassigned one raises NameError.
type pyLocals map[string]any

// get reads a local, raising NameError when it was never assigned.
func (l pyLocals) get(name string) any {
	v, ok := l[name]
	if !ok {
		raise("NameError", "%s", name)
	}
	return v
}

// intOrStr is "try: int(s) except: s".
func intOrStr(s string) any {
	if v, ok := tryInt(s); ok {
		return v
	}
	return s
}

// bgpState is ShowBgpSummarySuperParser.cli's local variables.
type bgpState struct {
	// sum is sum_dict, the result.
	sum *dict
	// vars holds the locals that may be read before they are assigned.
	vars pyLocals
	// passedVrf is the vrf argument; vrf and family the current ones.
	passedVrf, vrf, family string
}

// parseBgpSummary is ShowBgpSummarySuperParser.cli for a command whose
// output alone suffices: not an "rd" or "all summary" form, which make
// genieparser run a second command on the device. The address_family and
// vrf arguments are the command's.
func parseBgpSummary(out string, args map[string]string) *dict {
	s := &bgpState{sum: newDict(), vars: pyLocals{"cache_dict": newDict(), "entries_dict": newDict()},
		passedVrf: args["vrf"], vrf: args["vrf"], family: args["address_family"]}
	if s.vrf == "" {
		s.vrf = "default"
	}
	for _, raw := range splitlines(out) {
		s.line(strip(raw))
	}
	return s.sum
}

// line handles one stripped line, in genieparser's order.
func (s *bgpState) line(line string) {
	if m := bgpP1.match(line); m != nil {
		s.family = lowerStr(m.g("address_family"))
		if s.vrf == "" {
			s.vrf = "default"
		}
		for _, k := range []string{"attribute_entries", "num_prefix_entries", "path_total_entries", "total_memory",
			"activity_paths", "activity_prefixes", "scan_interval", "num_community_entries"} {
			s.vars[k] = ""
		}
		s.vars["cache_dict"], s.vars["entries_dict"] = newDict(), newDict()
		return
	}
	if s.statLine(line) {
		return
	}
	if m := bgpP9.match(line); m != nil {
		s.neighborLine(m)
		return
	}
	// A neighbor whose address is too long for its column is printed on
	// two lines: the address, then the rest.
	if m := bgpP10.match(line); m != nil {
		nbr := s.sum.sub("vrf").sub(s.vrf).sub("neighbor").sub(m.g("neighbor"))
		s.vars["nbr_af_dict"] = nbr.sub("address_family").sub(s.family)
	}
	if m := bgpP11.match(line); m != nil {
		s.fill(asDict(s.vars.get("nbr_af_dict")), m)
	}
}

// statLine handles p2 to p8, the table-wide counters.
func (s *bgpState) statLine(line string) bool {
	if m := bgpP2.match(line); m != nil {
		s.vars["route_identifier"] = m.g("route_identifier")
		s.vars["local_as"] = intOrStr(m.g("local_as"))
		s.sum.set("bgp_id", s.vars["local_as"])
		return true
	}
	if m := bgpP3.match(line); m != nil {
		s.vars["bgp_table_version"] = pyInt(m.g("bgp_table_version"))
		s.vars["routing_table_version"] = pyInt(m.g("routing_table_version"))
		return true
	}
	if m := bgpP4.match(line); m != nil {
		s.vars["num_prefix_entries"] = pyInt(m.g("networks"))
		s.vars["num_memory_usage"] = pyInt(m.g("bytes"))
		return true
	}
	if m := bgpP5.match(line); m != nil {
		s.vars["path_total_entries"] = pyInt(m.g("path"))
		s.vars["path_memory_usage"] = pyInt(m.g("memory_usage"))
		return true
	}
	if m := bgpP5_1.match(line); m != nil {
		asDict(s.vars.get("entries_dict")).set(m.g("entries_type"), bgpTotal(pyInt(m.g("num_entries")), pyInt(m.g("entries_byte"))))
		return true
	}
	if m := bgpP5_2.match(line); m != nil {
		s.vars["num_community_entries"] = pyInt(m.g("num_community_entries"))
		s.vars["community_memory_usage"] = pyInt(m.g("memory_usage"))
		return true
	}
	if m := bgpP6.match(line); m != nil {
		s.vars["attribute_entries"] = m.g("attribute_entries")
		return true
	}
	if m := bgpP6_1.match(line); m != nil {
		asDict(s.vars.get("cache_dict")).set(m.g("cache_type"), bgpTotal(pyInt(m.g("num_cache_entries")), pyInt(m.g("cache_byte"))))
		return true
	}
	if m := bgpP7.match(line); m != nil {
		s.vars["total_memory"] = pyInt(m.g("total_memory"))
		return true
	}
	if m := bgpP8.match(line); m != nil {
		s.vars["activity_prefixes"] = m.g("activity_prefixes")
		s.vars["activity_paths"] = m.g("activity_paths")
		s.vars["scan_interval"] = m.g("scan_interval")
		return true
	}
	return false
}

// bgpTotal is {'total_entries': total, 'memory_usage': memory}.
func bgpTotal(total, memory int64) *dict {
	d := newDict()
	d.set("total_entries", total)
	d.set("memory_usage", memory)
	return d
}

// neighborLine handles p9, a neighbor on one line.
func (s *bgpState) neighborLine(m *mo) {
	if s.passedVrf == "" && (s.family == "ipv4 unicast" || s.family == "ipv6 unicast") {
		s.vrf = "default"
	}
	nbr := s.sum.sub("vrf").sub(s.vrf).sub("neighbor").sub(m.g("neighbor"))
	af := nbr.sub("address_family").sub(s.family)
	s.vars["nbr_af_dict"] = af
	s.fill(af, m)
}

// fill records a neighbor's row and the table-wide counters on af. The
// counters are assigned inside genieparser's "try: ... except Exception:
// pass", so the first one never assigned stops the rest silently.
func (s *bgpState) fill(af *dict, m *mo) {
	af.set("version", pyInt(m.g("version")))
	af.set("as", intOrStr(m.g("as")))
	af.set("msg_rcvd", pyInt(m.g("msg_rcvd")))
	af.set("msg_sent", pyInt(m.g("msg_sent")))
	af.set("tbl_ver", pyInt(m.g("tbl_ver")))
	af.set("input_queue", pyInt(m.g("inq")))
	af.set("output_queue", pyInt(m.g("outq")))
	af.set("up_down", m.g("up_down"))
	af.set("state_pfxrcd", m.g("state"))
	for _, k := range []string{"route_identifier", "local_as", "bgp_table_version", "routing_table_version"} {
		af.set(k, s.vars.get(k))
	}
	try(func() { s.counters(af) })
}

// counters is the body of genieparser's try block.
func (s *bgpState) counters(af *dict) {
	v := s.vars
	if truthy(v.get("attribute_entries")) {
		af.set("attribute_entries", v["attribute_entries"])
	}
	s.pair(af, "prefixes", "num_prefix_entries", "num_memory_usage")
	s.pair(af, "path", "path_total_entries", "path_memory_usage")
	if truthy(v.get("total_memory")) {
		af.set("total_memory", v["total_memory"])
	}
	if truthy(v.get("activity_prefixes")) {
		af.set("activity_prefixes", v["activity_prefixes"])
	}
	if truthy(v.get("activity_paths")) {
		af.set("activity_paths", v["activity_paths"])
	}
	if truthy(v.get("scan_interval")) {
		af.set("scan_interval", pyInt(v["scan_interval"].(string)))
	}
	if asDict(v.get("cache_dict")).size() > 0 {
		af.set("cache_entries", v["cache_dict"])
	}
	if asDict(v.get("entries_dict")).size() > 0 {
		af.set("entries", v["entries_dict"])
	}
	s.pair(af, "community_entries", "num_community_entries", "community_memory_usage")
}

// pair sets af[key] to the total held in totalVar and the memory in
// memoryVar, when the total is set and not zero. The dict is assigned
// before the memory is read, as in genieparser.
func (s *bgpState) pair(af *dict, key, totalVar, memoryVar string) {
	if !truthy(s.vars.get(totalVar)) {
		return
	}
	d := newDict()
	af.set(key, d)
	d.set("total_entries", s.vars[totalVar])
	d.set("memory_usage", s.vars.get(memoryVar))
}
