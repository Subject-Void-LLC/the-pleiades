// Ported from genieparser's src/genie/libs/parser/iosxe/show_routing.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowIpRoute, continued: next-hop lines and the "show ip route <prefix>"
// detail lines.
package iosparse

import (
	"strconv"
	"strings"
)

// pathLines handles p4, p5 and p6: a further next hop of the current
// route.
func (s *routeState) pathLines(line string) bool {
	if m := routeP4.match(line); m != nil {
		s.preference(m.g("route_preference"))
		s.nextHop = m.g("next_hop")
		s.index++
		if v := m.g("interface"); v != "" {
			s.intf = v
		}
		if v := m.g("date"); v != "" {
			s.updated = v
		}
		s.record(true, true)
		return true
	}
	if m := routeP5.match(line); m != nil {
		s.preference(m.g("route_preference"))
		s.index++
		if v := m.g("next_hop"); v != "" {
			s.nextHop = v
		}
		if v := m.g("interface"); v != "" {
			s.intf = v
		}
		if v := m.g("date"); v != "" {
			s.updated = v
		}
		s.record(false, false)
		return true
	}
	if m := routeP6.match(line); m != nil {
		s.viaLine(m)
		return true
	}
	return false
}

// viaLine handles p6, "via 10.4.1.1%default, indirectly connected".
func (s *routeState) viaLine(m *mo) {
	vrfVal := ""
	tmp := m.g("next_hop")
	if tmp != "" {
		parts := strings.Split(tmp, "%")
		s.nextHop = parts[0]
		if len(parts) > 1 {
			vrfVal = parts[1]
		}
	}
	if v := m.g("interface"); v != "" {
		s.intf = v
	}
	s.index++
	d := s.routeEntry(true)
	nh := d.sub("next_hop")
	if s.nextHop == "" && s.intf != "" {
		addOutgoingInterface(nh, s.intf)
		return
	}
	if s.nextHop == "" {
		return
	}
	idx := nh.sub("next_hop_list").sub(strconv.FormatInt(s.index, 10))
	idx.set("index", s.index)
	idx.set("next_hop", s.nextHop)
	if s.updated != "" {
		idx.set("updated", s.updated)
	}
	if s.intf != "" {
		idx.set("outgoing_interface", s.intf)
		if contains(s.intf, "%") && contains(tmp, "%") && strings.Split(tmp, "%")[1] == strings.Split(s.intf, "%")[1] {
			idx.set("outgoing_interface", strings.Split(s.intf, "%")[0])
		}
	}
	if vrfVal != "" {
		idx.set("vrf", vrfVal)
	}
}

// detailLines handles p100 to p900, the lines of "show ip route <prefix>".
func (s *routeState) detailLines(line string) bool {
	if m := routeP100.match(line); m != nil {
		entry := s.out.sub("vrf").sub(s.vrf).sub("address_family").sub("ipv4")
		s.routeDict = entry.sub("routes").sub(s.route)
		s.routeDict.set("route", m.g("ip"))
		s.routeDict.set("mask", m.g("mask"))
		s.routeDict.set("active", true)
		return true
	}
	if m := routeP200.match(line); m != nil {
		d := s.current()
		d.set("distance", pyInt(m.g("distance")))
		d.set("metric", pyInt(m.g("metric")))
		if m.g("type") != "" {
			d.set("type", m.g("type"))
		}
		return true
	}
	if m := routeP300.match(line); m != nil {
		updateTruthy(s.current(), m)
		return true
	}
	if m := routeP400.match(line); m != nil {
		updateTruthy(s.current().sub("update"), m)
		return true
	}
	if m := routeP500.match(line); m != nil {
		s.index++
		s.pathDict = s.current().sub("next_hop").sub("next_hop_list").sub(strconv.FormatInt(s.index, 10))
		s.pathDict.set("index", s.index)
		s.pathDict.set("next_hop", m.v("nexthop"))
		s.pathDict.set("age", m.v("age"))
		s.pathDict.set("from", m.v("from"))
		s.pathDict.set("outgoing_interface", m.v("interface"))
		return true
	}
	// p600 does not end the line's handling, as in Python.
	if m := routeP600.match(line); m != nil {
		updateTruthy(s.path(), m)
	}
	for _, p := range []*rx{routeP700, routeP800, routeP900} {
		if m := p.match(line); m != nil {
			updateTruthy(s.path(), m)
			return true
		}
	}
	return false
}

// current is route_dict, NameError before any route line.
func (s *routeState) current() *dict {
	if s.routeDict == nil {
		raise("NameError", "route_dict")
	}
	return s.routeDict
}

// path is path_dict, NameError before any next-hop detail line.
func (s *routeState) path() *dict {
	if s.pathDict == nil {
		raise("NameError", "path_dict")
	}
	return s.pathDict
}
