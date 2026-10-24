// Ported from genieparser's src/genie/libs/parser/iosxe/show_vlan.py (commit
// e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its affiliates,
// Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowVlan: genieparser iosxe/show_vlan.py's ShowVlan.cli, for
// "show vlan".
package iosparse

import (
	"strconv"
	"strings"
)

// ShowVlan's expressions, named as genieparser names them.
var (
	vlanP0 = re(`^\s*(active|suspended|\w+/lshut|\w+/unsup)+.*$`)
	// genieparser writes the name as (?=\S).*(?<=\S): text that starts and
	// ends with a non-space character. RE2 has no lookaround, so the port
	// writes it \S(?:.*\S)?, which takes the same text in the same
	// longest-first order; the ShowVlan fixtures hold it.
	vlanP1 = re(`^(?P<vlan_id>[0-9]+)\s+(?P<name>\S(?:.*\S)?)` + `\s+(?P<status>(active|suspended|(.*)lshut|(.*)unsup)+)` + `(?P<interfaces>[\s\S]+)?$`)
	vlanP2 = re(`^\s*(?P<space>\s{48})(?P<interfaces>[\w\s\/\,]+)?$`)
	vlanP3 = re(`^\s*(?P<vlan_id>[0-9]+) +(?P<type>[a-zA-Z]+)` + ` +(?P<said>\d+) +(?P<mtu>[\d\-]+) +(?P<parent>[\w\-]+)?` + ` +(?P<ring_no>[\w\-]+)? +(?P<bridge_no>[\w\-]+)? +(?P<stp>[\w\-]+)?` + ` +(?P<bridge_mode>[\w\-]+)? +(?P<trans1>[\d\-]+) +(?P<trans2>[\d\-]+)$`)
	vlanP4 = re(`^\s*(?P<remote_span_vlans>[^--][0-9\-\,]+)?$`)
	vlanP5 = re(`^\s*(?P<primary>[0-9a-zA-Z]+)? +(?P<secondary>\d+)` + ` +(?P<type>[\w\-]+)( +(?P<interfaces>[\s\S]+))?`)
	vlanP6 = re(`^\s*(?P<vlan_id>\d+)\s+` + `(?P<are_hops>\d+)\s+` + `(?P<ste_hops>\d+)\s+` + `(?P<backup_crf>\S+)\s*$`)
)

// vlanRangeLimit is the widest remote SPAN range the port expands: every
// VLAN number IOS allows.
const vlanRangeLimit = 4094

// vlanState is ShowVlan.cli's local variables.
type vlanState struct {
	// out is vlan_dict.
	out *dict
	// prevLine is the last line no pattern handled, joined to a following
	// line that holds only a status.
	prevLine string
	// vlanID is the VLAN being read, when one has been seen.
	vlanID    string
	hasVlanID bool
}

// parseShowVlan is ShowVlan.cli.
func parseShowVlan(out string, _ map[string]string) *dict {
	s := &vlanState{out: newDict()}
	for _, raw := range splitlines(out) {
		if raw == "" {
			continue
		}
		line := rstrip(raw)
		// A name long enough to wrap leaves its status on the next line.
		if vlanP0.match(line) != nil {
			line = s.prevLine + " " + line
		}
		if !s.line(line) {
			s.prevLine = line
		}
	}
	return s.out
}

// line handles one line in genieparser's order and reports whether a
// pattern handled it.
func (s *vlanState) line(line string) bool {
	if m := vlanP1.match(line); m != nil {
		s.vlanLine(m)
		return true
	}
	if m := vlanP2.match(line); m != nil {
		vlan := s.out.d("vlans").d(s.id())
		vlan.set("interfaces", vlan.l("interfaces").concat(convertIntfList(m.g("interfaces"))))
		return true
	}
	if m := vlanP3.match(line); m != nil {
		s.typeLine(m)
		return true
	}
	if m := vlanP6.match(line); m != nil {
		s.vlanID, s.hasVlanID = m.g("vlan_id"), true
		ring := s.out.sub("vlans").sub(s.vlanID).sub("token_ring")
		ring.set("are_hops", pyInt(m.g("are_hops")))
		ring.set("ste_hops", pyInt(m.g("ste_hops")))
		ring.set("backup_crf", m.g("backup_crf"))
		return true
	}
	if m := vlanP4.match(line); m != nil {
		s.remoteSpan(m.g("remote_span_vlans"))
		return true
	}
	if m := vlanP5.match(line); m != nil {
		s.privateVlan(m)
		return true
	}
	return false
}

// id is vlan_id, NameError before any VLAN line.
func (s *vlanState) id() string {
	if !s.hasVlanID {
		raise("NameError", "vlan_id")
	}
	return s.vlanID
}

// convertIntfList splits a comma-separated port list and expands each
// abbreviated name.
func convertIntfList(ports string) *list {
	var names []string
	for _, p := range strings.Split(ports, ",") {
		names = append(names, convertIntfName(p))
	}
	return strList(names)
}

// vlanLine handles p1, "1 default active Gi1/0/1, Gi1/0/2".
func (s *vlanState) vlanLine(m *mo) {
	s.vlanID, s.hasVlanID = m.g("vlan_id"), true
	vlans := s.out.sub("vlans")
	// genieparser tests vlan_dict, not vlan_dict['vlans'], for the VLAN,
	// so the test always passes and the VLAN starts afresh.
	vlan := newDict()
	vlans.set(s.vlanID, vlan)
	vlan.set("vlan_id", s.vlanID)
	vlan.set("name", m.g("name"))
	vlan.set("shutdown", false)
	status := m.g("status")
	state := status
	switch {
	case contains(status, "act/unsup"):
		state = "unsupport"
	case contains(status, "suspend"):
		state = "suspend"
	case contains(status, "shut"):
		state = "shutdown"
		vlan.set("shutdown", true)
	}
	vlan.set("state", state)
	if m.g("interfaces") != "" {
		vlan.set("interfaces", convertIntfList(m.g("interfaces")))
	}
}

// typeLine handles p3, a row of the VLAN type table.
func (s *vlanState) typeLine(m *mo) {
	s.vlanID, s.hasVlanID = m.g("vlan_id"), true
	vlan := s.out.sub("vlans").sub(s.vlanID)
	vlan.set("type", m.g("type"))
	vlan.set("said", pyInt(m.g("said")))
	vlan.set("mtu", pyInt(m.g("mtu")))
	for _, k := range []string{"parent", "ring_no", "bridge_no", "stp", "bridge_mode"} {
		if !m.has(k) {
			// genieparser calls .strip() on the group, and None has none.
			raise("AttributeError", "'NoneType' object has no attribute 'strip'")
		}
		if !contains(strip(m.g(k)), "-") {
			vlan.set(k, m.g(k))
		}
	}
	vlan.set("trans1", pyInt(m.g("trans1")))
	vlan.set("trans2", pyInt(m.g("trans2")))
}

// remoteSpan handles p4, a list of remote SPAN VLANs: "201,202-205".
func (s *vlanState) remoteSpan(spans string) {
	if spans == "" {
		return
	}
	vlans := s.out.sub("vlans")
	for _, v := range strings.Split(spans, ",") {
		if !contains(v, "-") {
			vlans.sub(v).set("remote_span_vlan", true)
			continue
		}
		bounds := strings.Split(v, "-")
		if len(bounds) < 2 {
			raise("IndexError", "list index out of range")
		}
		start, end := pyInt(bounds[0]), pyInt(bounds[1])
		// genieparser would enumerate any range; no VLAN number passes
		// 4094, so a wider range is refused rather than walked.
		if end-start > vlanRangeLimit {
			raise(portLimit, "remote SPAN range %q spans more than %d VLANs", v, vlanRangeLimit)
		}
		for value := start; value <= end; value++ {
			vlans.sub(strconv.FormatInt(value, 10)).set("remote_span_vlan", true)
		}
	}
}

// privateVlan handles p5, a row of the private VLAN table.
func (s *vlanState) privateVlan(m *mo) {
	primary := ""
	named := m.g("primary") != "" && strings.ToLower(m.g("primary")) != "none"
	if named {
		primary = m.g("primary")
	}
	secondary := m.g("secondary")
	var ports *list
	if m.g("interfaces") != "" {
		ports = convertIntfList(m.g("interfaces"))
	}
	vlans := s.out.sub("vlans")
	if named {
		vlans.sub(primary).sub("private_vlan")
	}
	if primary != "" {
		pv := vlans.d(primary).d("private_vlan")
		pv.set("primary", true)
		if pv.has("association") {
			pv.set("association", pv.l("association").concat(newList(secondary)))
		} else {
			pv.set("association", strList(fields(secondary)))
		}
	}
	pv := vlans.sub(secondary).sub("private_vlan")
	pv.set("primary", false)
	pv.set("type", m.g("type"))
	if ports != nil {
		pv.set("ports", ports)
	}
}
