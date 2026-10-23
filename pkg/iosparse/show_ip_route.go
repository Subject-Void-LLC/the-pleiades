// Ported from genieparser's src/genie/libs/parser/iosxe/show_routing.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowIpRoute: genieparser iosxe/show_routing.py's ShowIpRoute.cli, for
// "show ip route".
package iosparse

import (
	"strconv"
	"strings"
)

// ShowIpRoute's expressions for IPv4, named as genieparser names them.
// The IPv6 subclass's p3 uses a lookahead; it is not ported.
var (
	routeP100 = re(`^Routing +entry +for +` + `(?P<entry>(?P<ip>[\w\:\.]+)\/(?P<mask>\d+))` + `(, +(?P<net>[\w\s]+))?$`)
	routeP200 = re(`^Known +via +\"(?P<known_via>[\w\s]+)\", +` + `distance +(?P<distance>\d+), +` + `metric +(?P<metric>\d+)` + `(, +type +(?P<type>[\w\-\s]+)(?P<connected>, connected)?)?$`)
	routeP300 = re(`^Redistributing +via +(?P<redist_via>\w+) *` + `(?P<redist_via_tag>\d+)?$`)
	routeP400 = re(`^Last +update +from +(?P<from>[\w\.]+) +` + `on +(?P<interface>[\w\.\/\-]+), +` + `(?P<age>[\w\.\:]+) +ago$`)
	routeP500 = re(`^\*? *(?P<nexthop>[\w\.]+)(, +` + `from +(?P<from>[\w\.]+), +` + `(?P<age>[\w\.\:]+) +ago, +` + `via +(?P<interface>[\w\.\/\-]+))?$`)
	routeP600 = re(`^Route +metric +is +(?P<metric>\d+), +` + `traffic +share +count +is +(?P<share_count>\d+)$`)
	routeP700 = re(`^Total +delay +is +(?P<total_delay>\d+) +microseconds, ` + `+minimum +bandwidth +is +(?P<minimum_bandwidth>\d+) +Kbit$`)
	routeP800 = re(`^Reliability +(?P<reliability>[\d\/]+), +minimum +MTU +(?P<minimum_mtu>\d+) +bytes$`)
	routeP900 = re(`^Loading +(?P<loading>[\d\/]+), Hops +(?P<hops>\d+)$`)
	routeP1   = re(`^Routing Table: +(?P<vrf>[\w?-]+)$`)
	routeP2   = re(`^(?P<subnetted_ip>[\d\/\.]+) +is +(variably )?subnetted, ` + `+(?P<number_of_subnets>[\d]+) +subnets(, +(?P<number_of_masks>[\d]+) +masks)?$`)
	routeP3   = re(`^(?P<code>[A-Za-z]{0,2}[0-9]*(\*[A-Za-z]{0,2}[0-9]*)?) +(?P<code1>(?:[A-Z][a-z]|[A-Z][\d]|[a-z]{2}|[A-Z]{2})(?:[+%&p])?|[+%&p])?\s*(?P<network>[0-9\.\:\/]+)?( ` + `+is +directly +connected,)? *\[?(?P<route_preference>[\d\/]+)?\]?,?(\s+tag\s(?P<tag_id>\d+))?( *(` + `via +)?(?P<next_hop>[\d\.]+))?,?( +\((?P<nh_vrf>[\w+\-]+)\))?,?( +(?P<date>[0-9][\w\:]+))?,?( +(?P<interface>[\S]+))?$`)
	routeP7   = re(`^(?P<code>[\w]+) +(?P<network>[\d\/\.]+)\s+\[(?P<route_preference>[\d\/]+)+\]+ via +(?P<next_hop>[0-9a-fA-F\:]+) +\((?P<nh_vrf>[\w\:]+)+\)+, +(?P<date>[dh\d\:]+)+(, +(?P<interface>[\w]+))?$`)
	routeP8   = re(`^(?P<code>[\w]+) +(?P<network>[\d\/\.][\S]+)$`)
	routeP9   = re(`^\[(?P<route_preference>[\d\/]+)+\]+ via +(?P<next_hop>[\d\:]+) +\((?P<nh_vrf>[\w\-\:]+)+\)+, +(?P<date>[\d\:]+)+, +(?P<interface>[\w\d]+)$`)
	routeP4   = re(`^\[(?P<route_preference>[\d\/]+)\] +via +(?P<next_hop>[\d\.]+)?,?` + `( +(?P<date>[0-9][\w\:]+),?)?( +(?P<interface>[\S]+))?$`)
	routeP5   = re(`^is +directly +connected,( +\[(?P<route_preference>[\d\/]+)\] ` + `+via +(?P<next_hop>[\d\.]+)?,)?( +(?P<date>[0-9][\w\:]+),)?` + `( +(?P<interface>[\S]+))?$`)
	routeP6   = re(`^via( +(?P<next_hop>(?:[\w]+[.:]|:+)[\w\:\.\%\-\_]{4,}),?)?` + `( +(?P<interface>[\w\.\/\-\_]+[\w\:\.\%\-\_]*),?)?,?( +receive)?` + `( +directly connected)?( +indirectly connected)?$`)
)

// routeProtocols is genieparser's source_protocol_dict: each protocol and
// the route codes that mean it, in genieparser's order. A code listed
// under several protocols means the last of them, since genieparser's
// loop does not stop at the first.
var routeProtocols = []struct {
	name  string
	codes []string
}{
	{"ospf", []string{"O", "OI", "OE1", "OE2", "ON1", "ON2", "IA", "N1", "N2", "E1", "E2", "+", "%", "p", "&"}},
	{"odr", []string{"o"}},
	{"isis", []string{"i", "su", "L1", "L2", "IA", "I1", "I2"}},
	{"eigrp", []string{"D", "EX", "+", "%", "p", "&"}},
	{"static", []string{"S", "+", "%", "p", "&"}},
	{"mobile", []string{"M"}},
	{"rip", []string{"R"}},
	{"lisp", []string{"l", "la", "lr", "ld", "lA", "le", "lp", "ls"}},
	{"nhrp", []string{"H"}},
	{"local", []string{"L"}},
	{"connected", []string{"C", "+", "%", "p", "&"}},
	{"local_connected", []string{"LC"}},
	{"bgp", []string{"B", "+", "%", "p", "&"}},
	{"nd", []string{"ND", "NDp"}},
	{"Per-user Static route", []string{"U"}},
	{"Destination", []string{"DCE"}},
	{"Redirect", []string{"NDr"}},
	{"omp", []string{"m"}},
	{"nat_dia", []string{"n", "Nd"}},
}

// routeState is ShowIpRoute.cli's local variables. Each Python local that
// may be read before it is assigned carries a flag, and reading it then
// raises NameError.
type routeState struct {
	// out is result_dict.
	out *dict
	// vrf is the VRF being read; route the route being read.
	vrf, route string
	// netmask is the mask of the last "is subnetted" line.
	netmask    string
	hasNetmask bool
	// codes and proto are source_protocol_codes and source_protocol.
	codes, proto       string
	hasCodes, hasProto bool
	// index numbers the current route's next hops.
	index int64
	// active is set once any route line is read.
	active bool
	// line1 holds a route line whose next hop is on the following line.
	line1 string
	// routeDict and pathDict are route_dict and path_dict, nil until set.
	routeDict, pathDict *dict
	// The per-line variables genieparser resets at the top of each line.
	nextHop, intf, updated, metrics, pref, nhVrf string
}

// parseShowIpRoute is ShowIpRoute.cli. The vrf argument names the VRF of
// output that has no "Routing Table:" line.
func parseShowIpRoute(out string, args map[string]string) *dict {
	s := &routeState{out: newDict(), vrf: args["vrf"]}
	if s.vrf == "" {
		s.vrf = "default"
	}
	for _, raw := range splitlines(out) {
		if raw == "" {
			continue
		}
		s.nextHop, s.intf, s.updated, s.metrics, s.pref, s.nhVrf = "", "", "", "", "", ""
		s.line(strip(raw))
	}
	return s.out
}

// line handles one stripped line, in genieparser's order.
func (s *routeState) line(line string) {
	if m := routeP1.match(line); m != nil {
		s.vrf = m.g("vrf")
		s.out.sub("vrf").sub(s.vrf)
		return
	}
	if m := routeP2.match(line); m != nil {
		s.active = true
		s.netmask, s.hasNetmask = "", true
		if ip := m.g("subnetted_ip"); ip != "" && contains(ip, "/") {
			s.netmask = strings.Split(ip, "/")[1]
		}
		return
	}
	if m := routeP3.match(line); m != nil {
		s.routeLine(m)
		return
	}
	if routeP8.match(line) != nil {
		s.line1 = line
	}
	if routeP9.match(line) != nil {
		s.splitRouteLine(line)
		return
	}
	if m := routeP7.match(line); m != nil {
		s.active = true
		s.codesAndNetwork(m)
		s.preference(m.g("route_preference"))
		s.nextHopFields(m)
		s.record(true, true)
		return
	}
	_ = s.pathLines(line) || s.detailLines(line)
}

// codesAndNetwork reads a route line's codes and network into the source
// protocol and the route.
func (s *routeState) codesAndNetwork(m *mo) {
	s.setCodes(m.g("code"))
	s.setNetwork(m.g("network"))
}

// setCodes records a route's codes, "S*" or "O", and the protocol they
// mean. An empty code changes nothing.
func (s *routeState) setCodes(code string) {
	if code == "" {
		return
	}
	s.codes, s.hasCodes = strip(code), true
	first := strings.Split(s.codes, "*")[0]
	for _, p := range routeProtocols {
		for _, c := range p.codes {
			if c == first {
				s.proto, s.hasProto = p.name, true
			}
		}
	}
}

// setNetwork makes network the current route, adding the mask of the last
// "is subnetted" line when it has none. An empty network changes nothing.
func (s *routeState) setNetwork(network string) {
	if network == "" {
		return
	}
	s.route = network
	if !contains(network, "/") {
		s.route = network + "/" + s.mask()
	}
}

// mask is netmask, raising NameError before any "is subnetted" line.
func (s *routeState) mask() string {
	if !s.hasNetmask {
		raise("NameError", "netmask")
	}
	return s.netmask
}

// preference splits "110/2" into the route preference and the metric.
func (s *routeState) preference(rp string) {
	if rp != "" && contains(rp, "/") {
		parts := strings.Split(rp, "/")
		s.pref, s.metrics = parts[0], parts[1]
	}
}

// nextHopFields reads a p7 or p9 match's next hop, interface, date and
// next-hop VRF.
func (s *routeState) nextHopFields(m *mo) {
	s.index = 0
	if nh := m.g("next_hop"); nh != "" {
		s.nextHop, s.index = nh, 1
	}
	if v, _ := m.opt("interface"); v != "" {
		s.intf = v
	}
	if v := m.g("date"); v != "" {
		s.updated = v
	}
	if v := m.g("nh_vrf"); v != "" {
		s.nhVrf = v
	}
}

// routeLine handles p3, the one-line route: codes, network, preference and
// the first next hop.
func (s *routeState) routeLine(m *mo) {
	s.active = true
	s.setCodes(m.g("code"))
	if code1 := m.g("code1"); code1 != "" {
		s.codes, s.hasCodes = s.sourceCodes()+" "+code1, true
	}
	s.setNetwork(m.g("network"))
	s.preference(m.g("route_preference"))
	s.index = 0
	if m.g("next_hop") != "" {
		s.nextHop, s.index = m.g("next_hop"), 1
	}
	s.intf, s.updated, s.nhVrf = m.g("interface"), m.g("date"), m.g("nh_vrf")
	s.record(true, true)
}

// sourceCodes is source_protocol_codes, NameError before any is read.
func (s *routeState) sourceCodes() string {
	if !s.hasCodes {
		raise("NameError", "source_protocol_codes")
	}
	return s.codes
}

// sourceProto is source_protocol, NameError before any is read.
func (s *routeState) sourceProto() string {
	if !s.hasProto {
		raise("NameError", "source_protocol")
	}
	return s.proto
}

// routeEntry is the current route's dict, created when missing, with its
// route set and, when withActive, its active flag.
func (s *routeState) routeEntry(withActive bool) *dict {
	s.routeDict = s.out.sub("vrf").sub(s.vrf).sub("address_family").sub("ipv4").sub("routes").sub(s.route)
	s.routeDict.set("route", s.route)
	if withActive {
		s.routeDict.set("active", s.active)
	}
	return s.routeDict
}

// record files the current line's preference, metric, codes and next hop
// on the route. withProto says whether this kind of line records the
// source protocol, and withActive whether it sets the active flag.
func (s *routeState) record(withProto, withActive bool) {
	d := s.routeEntry(withActive)
	if s.metrics != "" {
		d.set("metric", pyInt(s.metrics))
	}
	if s.pref != "" {
		d.set("route_preference", pyInt(s.pref))
	}
	if withProto && s.sourceCodes() != "" {
		d.set("source_protocol_codes", s.codes)
		d.set("source_protocol", s.sourceProto())
	}
	nh := d.sub("next_hop")
	if s.nextHop == "" && s.intf != "" {
		addOutgoingInterface(nh, s.intf)
	} else if s.nextHop != "" {
		idx := nh.sub("next_hop_list").sub(strconv.FormatInt(s.index, 10))
		idx.set("index", s.index)
		idx.set("next_hop", s.nextHop)
		if s.updated != "" {
			idx.set("updated", s.updated)
		}
		if s.intf != "" {
			idx.set("outgoing_interface", s.intf)
		}
		if s.nhVrf != "" {
			idx.set("vrf", s.nhVrf)
		}
	}
}

// addOutgoingInterface files an outgoing interface, splitting off the VRF
// IOS writes after a "%".
func addOutgoingInterface(nextHop *dict, intf string) {
	name, vrf, _ := strings.Cut(intf, "%")
	entry := nextHop.sub("outgoing_interface").sub(name)
	entry.set("outgoing_interface", name)
	if vrf != "" {
		entry.set("vrf", vrf)
	}
}

// splitRouteLine handles p9, the next-hop half of a route printed on two
// lines; line1 holds the first half.
func (s *routeState) splitRouteLine(line string) {
	if m := routeP8.match(s.line1); m != nil {
		s.active = true
		s.codesAndNetwork(m)
	}
	m := routeP9.match(line)
	if v := m.g("route_preference"); v != "" {
		s.preference(v)
	}
	s.nextHopFields(m)
	s.record(true, true)
}
