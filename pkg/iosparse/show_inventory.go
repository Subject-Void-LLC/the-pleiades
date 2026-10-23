// Ported from genieparser's src/genie/libs/parser/iosxe/show_platform.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowInventory: genieparser iosxe/show_platform.py's ShowInventory.cli,
// for "show inventory".
package iosparse

import "strings"

// ShowInventory's expressions, named as genieparser names them.
var (
	invP1  = re(`^NAME: +\"(?P<name>.*)\",` + ` +DESCR: +\"(?P<descr>.*)\"$`)
	invP11 = re(`^(Switch|[Mm]odule) +(?P<slot>(\S+))`)
	invP12 = re(`Power Supply Module`)
	invP13 = re(`^(SPA|IM|NIM|PVDM|SM) +subslot +(?P<slot>(\d+))/(?P<subslot>(\d+))`)
	invP14 = re(`^subslot +(?P<slot>(\d+))\/(?P<subslot>(.*))`)
	invP15 = re(`^StackPort(?P<slot>(\d+))/(?P<subslot>(\d+))$`)
	invP16 = re(`^Fan +Tray|\d+$`)
	invP17 = re(`^Modem +(?P<modem>\S+) +on +Cellular(?P<slot>\d+)\/(?P<subslot>.*)$`)
	invP18 = re(`^Slot \d Linecard|Slot \d Supervisor|Slot \d Router$`)
	invP2  = re(`^PID: +(?P<pid>[\S\s]+)? *, +VID:(?: +(?P<vid>(\S+)))? *,` + ` +SN:(?: +(?P<sn>(\S+)))?$`)
)

// invMainNames are the names whose entries ShowInventory files under main
// by their own name.
var invMainNames = []string{"Switch1", "Switch2", "GigabitEthernet", "TwoGigabitEthernet",
	"TenGigabitEthernet", "HundredGigE", "FourHundredGigE", "FiftyGigE"}

// invState is ShowInventory.cli's local variables. A dict variable that
// Python may read before assigning is nil until assigned, and reading it
// then raises NameError.
type invState struct {
	// ret is ret_dict, the result.
	ret *dict
	// name, descr, slot, subslot and pid are the current entry's fields.
	name, descr, slot, subslot, pid string
	// asr900RP records that an ASR-9 route processor was seen.
	asr900RP bool
	// slotDict, rpDict and lcDict are slot_dict, rp_dict and lc_dict.
	slotDict, rpDict, lcDict *dict
}

// parseShowInventory is ShowInventory.cli.
func parseShowInventory(out string, _ map[string]string) *dict {
	s := &invState{ret: newDict()}
	for _, raw := range splitlines(out) {
		line := strip(raw)
		if m := invP1.match(line); m != nil {
			s.nameLine(m)
			continue
		}
		if m := invP2.match(line); m != nil {
			s.pidLine(m)
		}
	}
	return s.ret
}

// nameLine handles a NAME/DESCR line: it records the entry's name and
// picks its slot.
func (s *invState) nameLine(m *mo) {
	s.name = strip(m.g("name"))
	s.descr = strip(m.g("descr"))
	if m1 := invP11.match(s.name); m1 != nil {
		s.slot = m1.g("slot")
		s.slotDict = s.ret.sub("slot").sub(s.slot)
	}
	if invP12.match(s.name) != nil {
		s.slot = strings.ReplaceAll(s.name, "Power Supply Module ", "P")
		s.slotDict = s.ret.sub("slot").sub(s.slot)
	}
	sub := invP13.match(s.name)
	if sub == nil {
		sub = invP14.match(s.name)
	}
	if sub == nil {
		sub = invP15.match(s.name)
	}
	if sub == nil {
		sub = invP17.match(s.name)
	}
	if sub != nil {
		s.slot = sub.g("slot")
		s.subslot = sub.g("subslot")
		s.slotDict = s.ret.sub("slot").sub(s.slot)
	}
	if invP16.match(s.name) != nil || invP18.match(s.name) != nil {
		s.slot = strings.ReplaceAll(s.name, " ", "_")
		s.slotDict = s.ret.sub("slot").sub(s.slot)
	}
}

// fill records the current entry's fields on d.
func (s *invState) fill(d *dict, vid, sn string) {
	d.set("name", s.name)
	d.set("descr", s.descr)
	d.set("pid", s.pid)
	d.set("vid", vid)
	d.set("sn", sn)
}

// pidLine handles a PID/VID/SN line: it files the current entry.
func (s *invState) pidLine(m *mo) {
	s.pid = ""
	if m.g("pid") != "" {
		s.pid = strings.Trim(m.g("pid"), " ")
	}
	vid, sn := m.g("vid"), m.g("sn")
	name, pid := s.name, s.pid
	if contains(name, "Chassis") {
		s.fill(s.ret.sub("main").sub("chassis").sub(pid), vid, sn)
	}
	if (contains(pid, "IE") || contains(pid, "17")) && contains(name, "Supervisor") {
		s.fill(s.ret.sub("main").sub("supervisor").sub(pid), vid, sn)
	}
	if contains(name, "Expansion Module") {
		s.fill(s.ret.sub("main").sub("expansion_module").sub(pid), vid, sn)
	}
	if contains(name, "cpu") || contains(name, "usb") {
		s.fill(s.ret.sub("main").sub(name).sub(pid), vid, sn)
	}
	for _, n := range invMainNames {
		if contains(name, n) {
			s.fill(s.ret.sub("main").sub(name).sub(pid), vid, sn)
		}
	}
	if contains(pid, "STACK") {
		s.ret.sub("main").set("swstack", true)
	}
	if contains(pid, "ASR-9") && !contains(pid, "PWR") && !contains(pid, "FAN") {
		s.rpDict = s.ret.sub("slot").sub("0").sub("rp").sub(pid)
		s.fill(s.rpDict, vid, sn)
		s.asr900RP = true
	}
	// Ensure name and slot have been parsed.
	if name == "" || s.slot == "" {
		return
	}
	s.fileInSlot(vid, sn)
	s.name, s.descr, s.slot, s.subslot = "", "", "", ""
}

// fileInSlot files the current entry under its slot as a route processor,
// a line card, a subslot or other.
func (s *invState) fileInSlot(vid, sn string) {
	name, pid := s.name, s.pid
	switch {
	case contains(pid, "RP") || contains(pid, "WS-C") || contains(name, "R"):
		s.rpDict = s.slot2().sub("rp").sub(pid)
		s.fill(s.rpDict, vid, sn)
	case (contains(pid, "SIP") || contains(pid, "-X") || contains(pid, "-HX") || contains(pid, "-LC") ||
		(contains(name, "module") && !contains(name, "module F"))) && !contains(name, "subslot"):
		s.lcDict = s.slot2().sub("lc").sub(pid)
		s.fill(s.lcDict, vid, sn)
	case s.subslot != "":
		var subslotDict *dict
		switch {
		case contains(pid, "STACK"):
			if s.rpDict == nil {
				stack := s.slot2().sub("other").sub(pid)
				subslotDict = stack.sub("subslot").sub(s.subslot).sub(pid)
			} else {
				subslotDict = s.rpDict.sub("subslot").sub(s.subslot).sub(pid)
			}
		case s.asr900RP:
			subslotDict = s.rp().sub("subslot").sub(s.subslot).sub(pid)
		default:
			if !s.slot2().has("lc") {
				s.lcDict = s.slot2().sub("lc").sub(pid)
			}
			subslotDict = s.lc().sub("subslot").sub(s.subslot).sub(pid)
		}
		s.fill(subslotDict, vid, sn)
	default:
		s.fill(s.slot2().sub("other").sub(pid), vid, sn)
	}
}

// slot2 is slot_dict, raising NameError before any slot was assigned.
func (s *invState) slot2() *dict {
	if s.slotDict == nil {
		raise("NameError", "slot_dict")
	}
	return s.slotDict
}

// rp is rp_dict, raising NameError before it was assigned.
func (s *invState) rp() *dict {
	if s.rpDict == nil {
		raise("NameError", "rp_dict")
	}
	return s.rpDict
}

// lc is lc_dict, raising NameError before it was assigned.
func (s *invState) lc() *dict {
	if s.lcDict == nil {
		raise("NameError", "lc_dict")
	}
	return s.lcDict
}
