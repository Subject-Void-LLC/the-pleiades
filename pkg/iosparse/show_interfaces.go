// Ported from genieparser's src/genie/libs/parser/iosxe/show_interface.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowInterfaces: genieparser iosxe/show_interface.py's ShowInterfaces.cli,
// for "show interfaces".
package iosparse

import (
	"strings"
)

// ShowInterfaces' expressions, named as genieparser names them.
var (
	intfP1    = re(`^(?P<interface>[\w\/\.\-\:]+) +is +(?P<enabled>[\w\s]+)(?: ` + `+\S+)?, +line +protocol +is +(?P<line_protocol>\w+)(?: ` + `*\((?P<attribute>\S+)\)|( +\, +Autostate +(?P<autostate>\S+)))?.*$`)
	intfP1_1  = re(`^(?P<interface>[\w\/\.\-\:]+) +is` + ` +(?P<enabled>[\w\s]+),` + ` +line +protocol +is +(?P<line_protocol>\w+)` + `( *, *(?P<attribute>[\w\s]+))?$`)
	intfP1_2  = re(`^(?P<interface>pseudowire\d+) +is +(?P<enabled>\w+)$`)
	intfP2    = re(`^Hardware +is +(?P<type>[a-zA-Z0-9\-\/\s\+\(\)\_]+?)` + `(, +address +is +(?P<mac_address>[a-fA-F0-9\.]+)` + ` *\(bia +(?P<phys_address>[a-fA-F0-9\.]+)\))?$`)
	intfP2_2  = re(`^Hardware +is +(?P<type>[a-zA-Z0-9\-\/\\_\+\(\) ]+?)` + `(, +address +is +(?P<mac_address>[a-fA-F0-9\.]+)` + ` *\(bia +(?P<phys_address>[a-fA-F0-9\.]+)\))?$`)
	intfP2_3  = re(`^Hardware +is +not +present$`)
	intfP3    = re(`^Description: *(?P<description>.*)$`)
	intfP4    = re(`^Secondary +Address +is +(?P<ipv4>(?P<ip>[0-9\.]+)` + `\/(?P<prefix_length>[0-9]+))$`)
	intfP5    = re(`^Internet +[A|a]ddress +is +(?P<ipv4>(?P<ip>[0-9\.x]+)` + `\/(?P<prefix_length>[0-9]+))$`)
	intfP6    = re(`^MTU +(?P<mtu>\d+) +bytes(, +sub +MTU +` + `(?P<sub_mtu>\d+))?, +BW +(?P<bandwidth>[0-9]+) +Kbit(\/sec)?` + `(, +RxBW +[0-9]+ +Kbit(\/sec)?)?, +` + `DLY +(?P<delay>[0-9]+) +usec,$`)
	intfP6_1  = re(`^MTU +(?P<mtu>\d+) +bytes, +BW +(?P<bandwidth>[\w\s]+)$`)
	intfP7    = re(`^reliability +(?P<reliability>[\d\/]+),` + ` +txload +(?P<txload>[\d\/]+), +rxload` + ` +(?P<rxload>[\d\/]+)$`)
	intfP8    = re(`^Encapsulation(\(s\):)? +(?P<encapsulation>[\w\s\.]+)` + `(, +(?P<rest>.*))?$`)
	intfP10   = re(`^Keepalive +set +\((?P<keepalive>[0-9]+)` + ` +sec\)$`)
	intfP11   = re(`^(?P<duplex_mode>\w+)[\-\s]+[d|D]uplex\, ` + `+(?P<port_speed>[\w\s\/]+|[a|A]uto-[S|s]peed|Auto ` + `(S|s)peed)(?:(?:\, +link +type +is ` + `+(?P<link_type>\S+))?(?:\, *(media +type +is| )` + `*(?P<media_type>[\w\/\-\.() ]+)?)(?: +media +type)?)?$`)
	intfP12   = re(`^(?P<first>input|output) +flow-control +is +(?P<receive>\w+), +` + `(?P<second>output|input) +flow-control +is +(?P<send>\w+)$`)
	intfP13   = re(`^ARP +type: +(?P<arp_type>\w+), +` + `ARP +Timeout +(?P<arp_timeout>[\w\:\.]+)$`)
	intfP14   = re(`^Last +input +(?P<last_input>[\w\.\:]+), +` + `output +(?P<last_output>[\w\.\:]+), ` + `output +hang +(?P<output_hang>[\w\.\:]+)$`)
	intfP15   = re(`^Members +in +this +channel: +` + `(?P<port_channel_member_intfs>[\w\/\.\s\,]+)$`)
	intfP15_1 = re(`^No\. +of +active +members +in +this +` + `channel: +(?P<active_members>\d+)$`)
	intfP15_2 = re(`^Member +\d+ +: +(?P<interface>\S+) +,` + ` +\S+, +\S+$`)
	intfP15_3 = re(`^No\. +of +PF_JUMBO +supported +members +` + `in +this +channel +: +(?P<number>\d+)$`)
	intfP16   = re(`^Last +clearing +of +\"show +interface\" +counters +` + `(?P<last_clear>[\w\:\.]+)$`)
	intfP17   = re(`^Input +queue: +(?P<size>\d+)\/(?P<max>\d+)\/` + `(?P<drops>\d+)\/(?P<flushes>\d+) +` + `\(size\/max\/drops\/flushes\); +` + `Total +output +drops: +(?P<output_drop>\d+)$`)
	intfP18   = re(`^Queueing +strategy: +(?P<queue_strategy>\S+).*$`)
	intfP19   = re(`^Output +queue: +(?P<size>\d+)\/(?P<max>\d+)` + `(?:\/(?P<threshold>\d+)\/(?P<drops>\d+))? ` + `+\(size\/max(?: +total\/threshold\/drops\))?.*$`)
	intfP20   = re(`^(?P<load_interval>[0-9\#]+)` + ` *(?P<unit>(minute|second|minutes|seconds)) *input *rate` + ` *(?P<in_rate>[0-9]+) *bits/sec,` + ` *(?P<in_rate_pkts>[0-9]+) *packets/sec$`)
	intfP21   = re(`^(?P<load_interval>[0-9\#]+)` + ` *(minute|second|minutes|seconds) *output *rate` + ` *(?P<out_rate>[0-9]+) *bits/sec,` + ` *(?P<out_rate_pkts>[0-9]+) *packets/sec$`)
	intfP35   = re(`^Interface +is +unnumbered. +Using +address +of +` + `(?P<unnumbered_intf>[\w\/\.]+) +` + `\((?P<unnumbered_ip>[\w\.\:]+)\)$`)
	intfP54   = re(`^Carrier +delay +is +(?P<carrier_delay>\d+).*$`)
	intfP55   = re(`^Asymmetric +Carrier-Delay +(?P<type>Down|Up)` + ` +Timer +is +(?P<carrier_delay>\d+).*$`)
	// The three expressions ShowInterfaces applies to the rest of an
	// Encapsulation line.
	intfEncapMedium = re(`(Vlan +ID +(?P<first_dot1q>[0-9]+),)?` + ` *medium +is +(?P<medium>[a-z0-9]+)$`)
	intfEncapQinQ   = re(`outer +ID +(?P<first>[0-9]+), +` + `inner +ID (?P<second>[0-9]+)$`)
	intfEncapVlan   = re(`Vlan +ID +(?P<first_dot1q>\d+).` + `|(?:,(?P<rest>[\s\w]+))$`)
	// intfUnnumberedIP splits an address and its prefix length.
	intfUnnumberedIP = re(`([\w\.\:]+)\/(\d+)`)
)

// intfState is ShowInterfaces.cli's local variables.
type intfState struct {
	// out is interface_dict, the result.
	out *dict
	// unnumbered is unnumbered_dict: each unnumbered interface and the
	// interface whose address it borrows.
	unnumbered *dict
	// intf is the interface being read, when a heading has been seen.
	intf    string
	hasIntf bool
	// lastClear is the counters' last clearing, when one has been seen.
	lastClear    string
	hasLastClear bool
	// section is section_name, "rx" or "tx" for the packet counters.
	section string
}

// cur is interface_dict[interface]: NameError before any heading, and
// KeyError if the heading's entry is missing.
func (s *intfState) cur() *dict {
	if !s.hasIntf {
		raise("NameError", "interface")
	}
	return s.out.d(s.intf)
}

// parseShowInterfaces is ShowInterfaces.cli.
func parseShowInterfaces(out string, _ map[string]string) *dict {
	s := &intfState{out: newDict(), unnumbered: newDict()}
	for _, raw := range splitlines(out) {
		line := strip(raw)
		_ = s.headLines(line) || s.addressLines(line) || s.linkLines(line) ||
			s.channelLines(line) || s.counterLines(line) || s.wanLines(line) ||
			s.tunnelLines(line) || s.x25Lines(line) || s.lapbLines(line)
	}
	s.fileUnnumbered()
	return s.out
}

// headLines handles the interface heading and the hardware lines.
func (s *intfState) headLines(line string) bool {
	m := intfP1.match(line)
	if m == nil {
		m = intfP1_1.match(line)
	}
	if m == nil {
		m = intfP1_2.match(line)
	}
	if m != nil {
		s.heading(m)
		return true
	}
	if intfP2_3.match(line) != nil {
		s.cur().set("is_present", false)
		return true
	}
	m = intfP2.match(line)
	if m == nil {
		m = intfP2_2.match(line)
	}
	if m != nil {
		s.cur().set("type", m.g("type"))
		if m.g("mac_address") != "" {
			s.cur().set("mac_address", m.g("mac_address"))
		}
		if m.g("phys_address") != "" {
			s.cur().set("phys_address", m.g("phys_address"))
		}
		return true
	}
	if m := intfP3.match(line); m != nil {
		s.cur().set("description", m.g("description"))
		return true
	}
	return false
}

// heading starts an interface: "GigabitEthernet1 is up, line protocol is
// up".
func (s *intfState) heading(m *mo) {
	s.intf, s.hasIntf = convertIntfName(m.g("interface")), true
	enabled := m.g("enabled")
	lineProtocol, _ := m.opt("line_protocol")
	attribute, _ := m.opt("attribute")
	autostate, _ := m.opt("autostate")
	autostate = strings.ToLower(autostate)
	if !s.out.has(s.intf) {
		d := newDict()
		pc := newDict()
		pc.set("port_channel_member", false)
		d.set("port_channel", pc)
		s.out.set(s.intf, d)
	}
	d := s.out.d(s.intf)
	d.set("is_deleted", contains(enabled, "deleted"))
	d.set("enabled", !(contains(enabled, "administratively down") || contains(enabled, "delete")))
	if lineProtocol != "" {
		d.set("line_protocol", lineProtocol)
		d.set("oper_status", lineProtocol)
	}
	if attribute != "" {
		d.set("connected", attribute == "connected")
		d.set("err_disabled", attribute == "err-disabled")
		d.set("suspended", attribute == "suspended")
	}
	if autostate != "" {
		d.set("autostate", autostate == "enabled")
	}
}

// addressLines handles the IPv4 address, MTU, reliability and
// encapsulation lines.
func (s *intfState) addressLines(line string) bool {
	if m := intfP4.match(line); m != nil {
		addr := s.cur().sub("ipv4").sub(m.g("ipv4"))
		addr.set("ip", m.g("ip"))
		addr.set("prefix_length", m.g("prefix_length"))
		addr.set("secondary", true)
		return true
	}
	if m := intfP5.match(line); m != nil {
		addr := s.cur().sub("ipv4").sub(m.g("ipv4"))
		addr.set("ip", m.g("ip"))
		addr.set("prefix_length", m.g("prefix_length"))
		return true
	}
	m := intfP6.match(line)
	if m == nil {
		m = intfP6_1.match(line)
	}
	if m != nil {
		if delay, _ := m.opt("delay"); delay != "" {
			s.cur().set("delay", pyInt(delay))
		}
		if m.g("mtu") != "" {
			s.cur().set("mtu", pyInt(m.g("mtu")))
		}
		if sub, _ := m.opt("sub_mtu"); sub != "" {
			s.cur().set("sub_mtu", pyInt(sub))
		}
		if bw := m.g("bandwidth"); bw != "" {
			if v, ok := tryInt(bw); ok {
				s.cur().set("bandwidth", v)
			} else {
				s.cur().set("bandwidth", bw)
			}
		}
		return true
	}
	if m := intfP7.match(line); m != nil {
		for _, k := range []string{"reliability", "txload", "rxload"} {
			s.cur().set(k, m.g(k))
		}
		return true
	}
	if m := intfP8.match(line); m != nil {
		s.encapsulation(m)
		return true
	}
	return false
}

// encapsulation handles an Encapsulation line and the VLAN tags the rest
// of it may carry.
func (s *intfState) encapsulation(m *mo) {
	encap := strings.ReplaceAll(strings.ToLower(m.g("encapsulation")), "802.1q virtual lan", "dot1q")
	encaps := s.cur().sub("encapsulations")
	encaps.set("encapsulation", encap)
	rest := m.g("rest")
	if rest == "" {
		return
	}
	m1 := intfEncapMedium.match(rest)
	m3 := intfEncapQinQ.match(rest)
	m4 := intfEncapVlan.match(rest)
	switch {
	case m1 != nil:
		if m1.g("first_dot1q") != "" {
			encaps.set("first_dot1q", m1.g("first_dot1q"))
		}
		// genieparser reads medium from the Encapsulation line's match,
		// which has no such group, so this raises KeyError in Genie too.
		s.cur().set("medium", m.g("medium"))
	case m3 != nil:
		encaps.set("first_dot1q", m3.g("first"))
		encaps.set("second_dot1q", m3.g("second"))
	case m4 != nil:
		if m4.g("first_dot1q") != "" {
			encaps.set("first_dot1q", m4.g("first_dot1q"))
		}
	}
}

// linkLines handles keepalive, duplex and speed, flow control, carrier
// delay, ARP and the last input and output times.
func (s *intfState) linkLines(line string) bool {
	if m := intfP10.match(line); m != nil {
		if m.g("keepalive") != "" {
			s.cur().set("keepalive", pyInt(m.g("keepalive")))
		}
		return true
	}
	if m := intfP11.match(line); m != nil {
		d := s.cur()
		d.set("duplex_mode", strings.ToLower(m.g("duplex_mode")))
		d.set("port_speed", strings.ReplaceAll(strings.ToLower(m.g("port_speed")), "-speed", ""))
		if link := m.g("link_type"); link != "" {
			d.set("link_type", link)
			d.set("auto_negotiate", contains(link, "auto"))
		}
		if media := m.g("media_type"); media != "" {
			if intfUnknown.search(media) != nil {
				d.set("media_type", "unknown")
			} else {
				d.set("media_type", media)
			}
		}
		return true
	}
	if m := intfP12.match(line); m != nil {
		s.flowControl(m)
		return true
	}
	if m := intfP54.match(line); m != nil {
		s.out.sub(s.name()).set("carrier_delay", pyInt(m.g("carrier_delay")))
		return true
	}
	if m := intfP55.match(line); m != nil {
		sub := s.out.sub(s.name())
		if strings.ToLower(m.g("type")) == "up" {
			sub.set("carrier_delay_up", pyInt(m.g("carrier_delay")))
		} else {
			sub.set("carrier_delay_down", pyInt(m.g("carrier_delay")))
		}
		return true
	}
	if m := intfP13.match(line); m != nil {
		s.cur().set("arp_type", strings.ToLower(m.g("arp_type")))
		s.cur().set("arp_timeout", m.g("arp_timeout"))
		return true
	}
	if m := intfP14.match(line); m != nil {
		for _, k := range []string{"last_input", "last_output", "output_hang"} {
			s.cur().set(k, m.g(k))
		}
		return true
	}
	return false
}

// intfUnknown finds "unknown" in a media type.
var intfUnknown = re(`[U|u]nknown`)

// name is the interface variable itself, NameError before any heading.
func (s *intfState) name() string {
	if !s.hasIntf {
		raise("NameError", "interface")
	}
	return s.intf
}

// flowControl handles "input flow-control is off, output flow-control is
// unsupported".
func (s *intfState) flowControl(m *mo) {
	receive := strings.ToLower(m.g("send"))
	if m.g("first") == "input" {
		receive = strings.ToLower(m.g("receive"))
	}
	send := strings.ToLower(m.g("receive"))
	if m.g("second") == "output" {
		send = strings.ToLower(m.g("send"))
	}
	fc := s.cur().sub("flow_control")
	setFlow(fc, "receive", receive)
	setFlow(fc, "send", send)
}

// setFlow records one direction of flow control: on is true, off or
// unsupported is false, anything else is left out.
func setFlow(fc *dict, key, value string) {
	switch {
	case contains(value, "on"):
		fc.set(key, true)
	case contains(value, "off") || contains(value, "unsupported"):
		fc.set(key, false)
	}
}
