// Ported from genieparser's src/genie/libs/parser/iosxe/show_interface.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowInterfaces, continued: port channels, queues, counters, WAN,
// tunnel, X.25, LAPB and serial lines, and unnumbered interfaces.
package iosparse

import "strings"

// More of ShowInterfaces' expressions, named as genieparser names them.
var (
	intfP22 = re(`^(?P<in_pkts>[0-9]+) +packets +input, +(?P<in_octets>[0-9]+) ` + `+bytes(?:, +(?P<in_no_buffer>[0-9]+) +no +buffer)?$`)
	intfP23 = re(`^Received +(?P<in_broadcast_pkts>\d+) +broadcasts +` + `\((?P<in_multicast_pkts>\d+) *(IP)? *multicasts\)$`)
	intfP24 = re(`^(?P<in_runts>[0-9]+) *runts,` + ` *(?P<in_giants>[0-9]+) *giants,` + ` *(?P<in_throttles>[0-9]+) *throttles$`)
	intfP25 = re(`^(?P<in_errors>[0-9]+) +input +errors, +` + `(?P<in_crc_errors>[0-9]+) +CRC, +` + `(?P<in_frame>[0-9]+) +frame, +` + `(?P<in_overrun>[0-9]+) +overrun, +` + `(?P<in_ignored>[0-9]+) +ignored` + `(, *(?P<in_abort>[0-9]+) +abort)?$`)
	intfP26 = re(`^(?P<in_watchdog>[0-9]+) +watchdog, +` + `(?P<in_multicast_pkts>[0-9]+) +multicast, +` + `(?P<in_pause_input>[0-9]+) +pause +input$`)
	intfP27 = re(`^(?P<in_with_dribble>[0-9]+) +input +packets +with +` + `dribble +condition +detected$`)
	intfP28 = re(`^(?P<out_pkts>[0-9]+) +packets +output, +(?P<out_octets>[0-9]+) ` + `+bytes(?:\, +(?P<out_underruns>[0-9]+) +underruns)?$`)
	intfP29 = re(`^Output +(?P<out_broadcast_pkts>\d+) +broadcasts +` + `\((?P<out_multicast_pkts>\d+) *(IP)? *multicasts\)$`)
	intfP30 = re(`^(?P<out_errors>[0-9]+) +output +errors,` + `( *(?P<out_collision>[0-9]+) +collisions,)? +` + `(?P<out_interface_resets>[0-9]+) +interface +resets$`)
	intfP31 = re(`^(?P<out_unknown_protocl_drops>[0-9]+) +` + `unknown +protocol +drops$`)
	intfP32 = re(`^(?P<out_babble>[0-9]+) +babbles, +` + `(?P<out_late_collision>[0-9]+) +late +collision, +` + `(?P<out_deferred>[0-9]+) +deferred$`)
	intfP33 = re(`^(?P<out_lost_carrier>\d+) +lost +carrier, +` + `(?P<out_no_carrier>\d+) +no +carrier(, +(?P<out_pause_output>\d+) +` + `pause +output)?$`)
	intfP34 = re(`^(?P<out_buffer_failure>[0-9]+) +output +buffer +failures, +` + `(?P<out_buffers_swapped>[0-9]+) +output +buffers +swapped +out$`)
	intfP36 = re(`^(?P<maximum_active_vcs>\d+) +maximum +active +VCs, +` + `(?P<vcs_per_vp>\d+) +VCs +per +VP, +(?P<current_vccs>\d+) +current +VCCs$`)
	intfP37 = re(`^VC +Auto +Creation +(?P<vc_auto_creation>\S+)\.$`)
	intfP38 = re(`^VC +idle +disconnect +time: +(?P<vc_idle_disconnect_time>\d+) +` + `seconds$`)
	intfP39 = re(`^(?P<key>\S+ +CRC +errors) +: +(?P<val>\d+)$`)
	intfP40 = re(`^(?P<key>\S+ +SAR +Timeouts) +: +(?P<val>\d+)$`)
	intfP41 = re(`^(?P<key>\S+ +Oversized +SDUs) +: +(?P<val>\d+)$`)
	intfP42 = re(`^LCP\s+(?P<state>\S+)(,\s+loopback\s+(?P<loopback>[\S\s]+))?$`)
	intfP43 = re(`^Base PPPoATM +(?P<base_pppoatm>\S+)$`)
	intfP44 = re(`^Vaccess\s+status\s+(?P<status>\S+),\s+` + `loopback\s+(?P<loopback>[\S\s]+)$`)
	intfP45 = re(`^DTR +is +pulsed +for +(?P<dtr_pulsed>\d+) +` + `seconds +on +reset$`)
	intfP46 = re(`^Tunnel +source +(?P<tunnel_source_ip>([a-fA-F\d\:UNKNOWN|0-9\.]+)?),?\s?` + `(?P<tunnel_source_interface>\([\w\d.\/]+\))?,?\s?` + `(destination +)?(?P<tunnel_destination_ip>([a-fA-F\d\:0-9\.]+)?)`)
	intfP47 = re(`^Tunnel +protocol/transport +(?P<tunnel_protocol>[\w\/]+)`)
	intfP52 = re(`^Tunnel +protection +via +(?P<tunnel_protection>[\w]+) +\(profile \"(?P<tunnel_profile>[\w]+)\"\)`)
	intfP53 = re(`^(?P<carrier_transitions>\d+)\s+carrier transitions$`)
	intfP56 = re(`^Peer IP (?P<peer_ip>[\d\.]+), VC ID (?P<vc_id>\d+)$`)
	intfP57 = re(`^(?P<rx_tx>RX|TX)$`)
	intfP58 = re(`^(?P<pkts>\d+) packets (?P<octets>\d+) bytes (?P<drops>\d+) drops$`)
)

// intfTunnelInts are p48 to p51: a tunnel line whose one number is
// recorded as an int under the group's own name.
var intfTunnelInts = []*rx{
	re(`^Tunnel +TTL +(?P<tunnel_ttl>\d+)`),
	re(`^Tunnel +transport +MTU +(?P<tunnel_transport_mtu>\d+)`),
	re(`^Tunnel +transmit +bandwidth +(?P<tunnel_transmit_bandwidth>\d+)`),
	re(`^Tunnel +receive +bandwidth +(?P<tunnel_receive_bandwidth>\d+)`),
}

// intfTunnelIntKeys are the result keys of intfTunnelInts, in order.
var intfTunnelIntKeys = []string{"tunnel_ttl", "tunnel_transport_mtu", "tunnel_transmit_bandwidth", "tunnel_receive_bandwidth"}

// channelLines handles the port channel, last clearing and queue lines.
func (s *intfState) channelLines(line string) bool {
	if m := intfP15.match(line); m != nil {
		pc := s.cur().d("port_channel")
		pc.set("port_channel_member", true)
		var intfs []string
		for _, i := range strings.Split(m.g("port_channel_member_intfs"), " ") {
			intfs = append(intfs, convertIntfName(strip(i)))
		}
		pc.set("port_channel_member_intfs", strList(intfs))
		for _, intf := range intfs {
			member := s.out.sub(intf).sub("port_channel")
			member.set("port_channel_member", true)
			member.set("port_channel_int", s.intf)
		}
		return true
	}
	if m := intfP15_1.match(line); m != nil {
		active := pyInt(m.g("active_members"))
		pc := s.cur().d("port_channel")
		pc.set("port_channel_member", true)
		pc.set("active_members", active)
		return true
	}
	if m := intfP15_2.match(line); m != nil {
		pc := s.cur().d("port_channel")
		asList(pc.setdefault("port_channel_member_intfs", newList())).appendItem(m.g("interface"))
		return true
	}
	if m := intfP15_3.match(line); m != nil {
		s.cur().d("port_channel").set("num_of_pf_jumbo_supported_members", pyInt(m.g("number")))
		return true
	}
	if m := intfP16.match(line); m != nil {
		s.lastClear, s.hasLastClear = m.g("last_clear"), true
		return true
	}
	if m := intfP17.match(line); m != nil {
		q := s.cur().sub("queues")
		q.set("input_queue_size", pyInt(m.g("size")))
		q.set("input_queue_max", pyInt(m.g("max")))
		q.set("input_queue_drops", pyInt(m.g("drops")))
		q.set("input_queue_flushes", pyInt(m.g("flushes")))
		q.set("total_output_drop", pyInt(m.g("output_drop")))
		return true
	}
	if m := intfP18.match(line); m != nil {
		s.cur().sub("queues").set("queue_strategy", m.g("queue_strategy"))
		return true
	}
	if m := intfP19.match(line); m != nil {
		q := s.cur().sub("queues")
		q.set("output_queue_size", pyInt(m.g("size")))
		q.set("output_queue_max", pyInt(m.g("max")))
		if m.g("threshold") != "" && m.g("drops") != "" {
			q.set("threshold", pyInt(m.g("threshold")))
			q.set("drops", pyInt(m.g("drops")))
		}
		return true
	}
	return false
}

// counterLines handles the rate and packet counter lines.
func (s *intfState) counterLines(line string) bool {
	if m := intfP20.match(line); m != nil {
		load := pyInt(m.g("load_interval"))
		inRate := pyInt(m.g("in_rate"))
		inRatePkts := pyInt(m.g("in_rate_pkts"))
		if contains(m.g("unit"), "minute") {
			load *= 60
		}
		counters := s.cur().sub("counters")
		rate := counters.sub("rate")
		rate.set("load_interval", load)
		rate.set("in_rate", inRate)
		rate.set("in_rate_pkts", inRatePkts)
		if !counters.has("last_clear") && s.hasLastClear {
			counters.set("last_clear", s.lastClear)
		}
		return true
	}
	if m := intfP21.match(line); m != nil {
		d := s.cur()
		if !d.has("counters") {
			counters := newDict()
			counters.set("rate", newDict())
			d.set("counters", counters)
		}
		rate := d.d("counters").d("rate")
		rate.set("out_rate", pyInt(m.g("out_rate")))
		rate.set("out_rate_pkts", pyInt(m.g("out_rate_pkts")))
		return true
	}
	if m := intfP22.match(line); m != nil {
		counters := s.cur().sub("counters")
		counters.set("in_pkts", pyInt(m.g("in_pkts")))
		counters.set("in_octets", pyInt(m.g("in_octets")))
		if m.g("in_no_buffer") != "" {
			counters.set("in_no_buffer", pyInt(m.g("in_no_buffer")))
		}
		return true
	}
	for _, c := range intfCounterLines {
		if m := c.re.match(line); m != nil {
			counters := s.cur().d("counters")
			for _, f := range c.fields {
				if f[2] == "optional" && m.g(f[1]) == "" {
					continue
				}
				counters.set(f[0], pyInt(m.g(f[1])))
			}
			return true
		}
	}
	return false
}

// intfCounter is one counter line and the counters it sets: each field is
// the result key, the group name, and "optional" for a group that is set
// only when it matched.
type intfCounter struct {
	// re matches the line.
	re *rx
	// fields are the counters it sets, in genieparser's order.
	fields [][3]string
}

// intfCounterLines are p23 to p34, in genieparser's order.
var intfCounterLines = []intfCounter{
	{intfP23, [][3]string{{"in_multicast_pkts", "in_multicast_pkts"}, {"in_broadcast_pkts", "in_broadcast_pkts"}}},
	{intfP24, [][3]string{{"in_runts", "in_runts"}, {"in_giants", "in_giants"}, {"in_throttles", "in_throttles"}}},
	{intfP25, [][3]string{{"in_errors", "in_errors"}, {"in_crc_errors", "in_crc_errors"}, {"in_frame", "in_frame"},
		{"in_overrun", "in_overrun"}, {"in_ignored", "in_ignored"}, {"in_abort", "in_abort", "optional"}}},
	{intfP26, [][3]string{{"in_watchdog", "in_watchdog"}, {"in_multicast_pkts", "in_multicast_pkts"}, {"in_mac_pause_frames", "in_pause_input"}}},
	{intfP27, [][3]string{{"in_with_dribble", "in_with_dribble"}}},
	{intfP28, [][3]string{{"out_pkts", "out_pkts"}, {"out_octets", "out_octets"}, {"out_underruns", "out_underruns", "optional"}}},
	{intfP29, [][3]string{{"out_broadcast_pkts", "out_broadcast_pkts"}, {"out_multicast_pkts", "out_multicast_pkts"}}},
	{intfP30, [][3]string{{"out_errors", "out_errors"}, {"out_interface_resets", "out_interface_resets"}, {"out_collision", "out_collision", "optional"}}},
	{intfP31, [][3]string{{"out_unknown_protocl_drops", "out_unknown_protocl_drops"}}},
	{intfP32, [][3]string{{"out_babble", "out_babble"}, {"out_late_collision", "out_late_collision"}, {"out_deferred", "out_deferred"}}},
	{intfP33, [][3]string{{"out_lost_carrier", "out_lost_carrier"}, {"out_no_carrier", "out_no_carrier"}, {"out_mac_pause_frames", "out_pause_output", "optional"}}},
	{intfP34, [][3]string{{"out_buffer_failure", "out_buffer_failure"}, {"out_buffers_swapped", "out_buffers_swapped"}}},
}

// wanLines handles the unnumbered, ATM, PPP and DTR lines.
func (s *intfState) wanLines(line string) bool {
	if m := intfP35.match(line); m != nil {
		entry := newDict()
		entry.set("unnumbered_intf", m.g("unnumbered_intf"))
		entry.set("unnumbered_ip", m.g("unnumbered_ip"))
		s.unnumbered.set(s.name(), entry)
		return true
	}
	if m := intfP36.match(line); m != nil {
		for _, k := range []string{"maximum_active_vcs", "vcs_per_vp", "current_vccs"} {
			s.cur().set(k, m.g(k))
		}
		return true
	}
	if m := intfP37.match(line); m != nil {
		s.cur().set("vc_auto_creation", m.g("vc_auto_creation"))
		return true
	}
	if m := intfP38.match(line); m != nil {
		s.cur().set("vc_idle_disconnect_time", m.g("vc_idle_disconnect_time"))
		return true
	}
	// genieparser files the SAR and oversized counts under each other's
	// names; the port keeps that.
	for i, p := range []*rx{intfP39, intfP40, intfP41} {
		if m := p.match(line); m != nil {
			s.cur().set([]string{"aal5_crc_errors", "aal5_oversized_sdus", "aal5_sar_timeouts"}[i], pyInt(m.g("val")))
			return true
		}
	}
	if m := intfP42.match(line); m != nil {
		s.cur().set("lcp_state", m.g("state"))
		if lb, _ := m.opt("loopback"); lb != "" {
			s.cur().set("lcp_loopack", lb)
		}
		return true
	}
	if m := intfP43.match(line); m != nil {
		s.cur().set("base_pppoatm", m.g("base_pppoatm"))
		return true
	}
	if m := intfP44.match(line); m != nil {
		s.cur().set("vaccess_status", m.g("status"))
		s.cur().set("vaccess_loopback", m.g("loopback"))
		return true
	}
	if m := intfP45.match(line); m != nil {
		s.cur().set("dtr_pulsed", m.g("dtr_pulsed"))
		return true
	}
	return false
}

// tunnelLines handles the tunnel, carrier transition, pseudowire and RX/TX
// counter lines.
func (s *intfState) tunnelLines(line string) bool {
	if m := intfP46.match(line); m != nil {
		s.cur().set("tunnel_source_ip", m.g("tunnel_source_ip"))
		if src := m.g("tunnel_source_interface"); src != "" {
			s.cur().set("tunnel_source_interface", strings.Trim(src, "()"))
		}
		if dst := m.g("tunnel_destination_ip"); dst != "" {
			s.cur().set("tunnel_destination_ip", dst)
		}
		return true
	}
	if m := intfP47.match(line); m != nil {
		s.cur().set("tunnel_protocol", m.g("tunnel_protocol"))
		return true
	}
	for i, p := range intfTunnelInts {
		if m := p.match(line); m != nil {
			s.cur().set(intfTunnelIntKeys[i], pyInt(m.g(intfTunnelIntKeys[i])))
			return true
		}
	}
	if m := intfP52.match(line); m != nil {
		for _, k := range []string{"tunnel_protection", "tunnel_profile"} {
			if m.g(k) != "" {
				s.cur().set(k, m.g(k))
			}
		}
		return true
	}
	if m := intfP53.match(line); m != nil {
		s.cur().set("carrier_transitions", pyInt(m.g("carrier_transitions")))
		return true
	}
	if m := intfP56.match(line); m != nil {
		s.cur().set("peer_ip", m.g("peer_ip"))
		s.cur().set("vc_id", pyInt(m.g("vc_id")))
		return true
	}
	if m := intfP57.match(line); m != nil {
		s.section = strings.ToLower(m.g("rx_tx"))
		return true
	}
	if m := intfP58.match(line); m != nil {
		counters := s.cur().sub("counters")
		direction := "out"
		if s.section == "rx" {
			direction = "in"
		}
		counters.set(direction+"_pkts", pyInt(m.g("pkts")))
		counters.set(direction+"_octets", pyInt(m.g("octets")))
		counters.set(direction+"_drops", pyInt(m.g("drops")))
		return true
	}
	return false
}

// fileUnnumbered gives each unnumbered interface the address it borrows.
func (s *intfState) fileUnnumbered() {
	for _, intf := range s.unnumbered.keys {
		entry := s.unnumbered.d(intf)
		ref := asString(entry.get("unnumbered_intf"))
		ip := asString(entry.get("unnumbered_ip"))
		owner, ok := s.out.get(ref).(*dict)
		if !ok || !owner.has("ipv4") {
			continue
		}
		addrs := owner.d("ipv4")
		size := addrs.size()
		for _, addr := range addrs.keyList() {
			if contains(addr, ip) {
				s.borrow(intf, addr, ref)
			}
			// Python refuses to go on iterating a dict whose size changed,
			// which happens only when an interface is unnumbered to itself.
			if addrs.size() != size {
				raise("RuntimeError", "dictionary changed size during iteration")
			}
		}
	}
}

// borrow records on intf the address addr it borrows from ref.
func (s *intfState) borrow(intf, addr, ref string) {
	ipv4 := s.out.d(intf).sub("ipv4")
	entry := ipv4.sub(addr)
	m := intfUnnumberedIP.search(addr)
	if m == nil {
		raise("AttributeError", "'NoneType' object has no attribute 'groups'")
	}
	entry.set("ip", m.n(1))
	entry.set("prefix_length", m.n(2))
	unnumbered := newDict()
	unnumbered.set("interface_ref", ref)
	ipv4.set("unnumbered", unnumbered)
}
