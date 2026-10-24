// Ported from genieparser's src/genie/libs/parser/iosxe/show_interface.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowInterfaces, continued: the X.25, LAPB and serial signal lines.
package iosparse

// ShowInterfaces' X.25, LAPB and serial expressions, named as genieparser
// names them.
var (
	intfP59 = re(`^X\.25 +(?P<dte>DTE), +version +(?P<version>\d+), +address +(?P<address>\d+), +state +(?P<state>\w+), +modulo +(?P<modulo>\d+), +timer +(?P<timer>\d+)$`)
	intfP60 = re(`^Defaults: +idle +VC +timeout +(?P<idle_vc_timeout>\d+)$`)
	intfP61 = re(`^(?P<encapsulation>cisco) +encapsulation$`)
	intfP62 = re(`^input/output +window +sizes +(?P<input_window>\d+)/(?P<output_window>\d+), +packet +sizes +(?P<input_packet>\d+)/(?P<output_packet>\d+)$`)
	intfP63 = re(`^Timers: +T20 +(?P<t20>\d+), +T21 +(?P<t21>\d+), +T22 +(?P<t22>\d+), +T23 +(?P<t23>\d+)$`)
	intfP64 = re(`^Channels: +Incoming-only +(?P<incoming_only>\w+), +Two-way +(?P<two_way>[\d\-]+), +Outgoing-only +(?P<outgoing_only>\w+)$`)
	intfP65 = re(`^RESTARTs +(?P<restarts>[\d\/]+) +CALLs +(?P<calls>[\d\+\/]+) +DIAGs +(?P<diags>[\d\/]+)$`)
	intfP66 = re(`^LAPB +(?P<dte>DTE), +state +(?P<state>\w+), +modulo +(?P<modulo>\d+), +k +(?P<k>\d+), +N1 +(?P<n1>\d+), +N2 +(?P<n2>\d+)$`)
	intfP67 = re(`^T1 +(?P<t1>\d+), +T2 +(?P<t2>\d+), +interface +outage +\(partial +T3\) +(?P<t3>\d+), +T4 +(?P<t4>\d+)$`)
	intfP68 = re(`^VS +(?P<vs>\d+), +VR +(?P<vr>\d+), +tx +NR +(?P<tx_nr>\d+), +Remote +VR +(?P<remote_vr>\d+), +Retransmissions +(?P<retransmissions>\d+)$`)
	intfP69 = re(`^Queues: +U/S +frames +(?P<u_s_frames>\d+), +I +frames +(?P<i_frames>\d+), +unack\. +(?P<unack>\d+), +reTx +(?P<retx>\d+)$`)
	intfP70 = re(`^IFRAMEs +(?P<iframes>[\d\/]+) +RNRs +(?P<rnrs>[\d\/]+) +REJs +(?P<rejs>[\d\/]+) +SABM/Es +(?P<sabm_es>[\d\/]+) +FRMRs +(?P<frmrs>[\d\/]+) +DISCs +(?P<discs>[\d\/]+)$`)
	intfP71 = re(`^RTS +(?P<rts>\w+), +CTS +(?P<cts>\w+), +DTR +(?P<dtr>\w+), +DCD +(?P<dcd>\w+), +DSR +(?P<dsr>\w+)$`)
)

// setInts sets each named group of m on d as an int, under the key the
// matching position of keys gives (the group name when keys is nil).
func setInts(d *dict, m *mo, groups []string, keys []string) {
	for i, g := range groups {
		k := g
		if keys != nil {
			k = keys[i]
		}
		d.set(k, pyInt(m.g(g)))
	}
}

// setStrs sets each named group of m on d as a string under its own name.
func setStrs(d *dict, m *mo, groups ...string) {
	for _, g := range groups {
		d.set(g, m.g(g))
	}
}

// x25Lines handles the X.25 lines.
func (s *intfState) x25Lines(line string) bool {
	if m := intfP59.match(line); m != nil {
		x := s.cur().sub("x25")
		x.set("dte", m.g("dte") == "DTE")
		setStrs(x, m, "version", "address", "state")
		setInts(x, m, []string{"modulo", "timer"}, nil)
		return true
	}
	if m := intfP60.match(line); m != nil {
		setInts(s.cur().sub("x25"), m, []string{"idle_vc_timeout"}, nil)
		return true
	}
	if m := intfP61.match(line); m != nil {
		setStrs(s.cur().sub("x25"), m, "encapsulation")
		return true
	}
	if m := intfP62.match(line); m != nil {
		setInts(s.cur().sub("x25"), m, []string{"input_window", "output_window", "input_packet", "output_packet"},
			[]string{"input_window_size", "output_window_size", "input_packet_size", "output_packet_size"})
		return true
	}
	if m := intfP63.match(line); m != nil {
		setInts(s.cur().sub("x25").sub("timers"), m, []string{"t20", "t21", "t22", "t23"}, nil)
		return true
	}
	if m := intfP64.match(line); m != nil {
		setStrs(s.cur().sub("x25").sub("channels"), m, "incoming_only", "two_way", "outgoing_only")
		return true
	}
	if m := intfP65.match(line); m != nil {
		setStrs(s.cur().sub("x25"), m, "restarts", "calls", "diags")
		return true
	}
	return false
}

// lapbLines handles the LAPB and serial signal lines.
func (s *intfState) lapbLines(line string) bool {
	if m := intfP66.match(line); m != nil {
		l := s.cur().sub("lapb")
		l.set("dte", m.g("dte") == "DTE")
		l.set("state", m.g("state"))
		setInts(l, m, []string{"modulo", "k", "n1", "n2"}, nil)
		return true
	}
	if m := intfP67.match(line); m != nil {
		setInts(s.cur().sub("lapb"), m, []string{"t1", "t2", "t3", "t4"}, nil)
		return true
	}
	if m := intfP68.match(line); m != nil {
		setInts(s.cur().sub("lapb"), m, []string{"vs", "vr", "tx_nr", "remote_vr", "retransmissions"}, nil)
		return true
	}
	if m := intfP69.match(line); m != nil {
		setInts(s.cur().sub("lapb").sub("queues"), m, []string{"u_s_frames", "i_frames", "unack", "retx"}, nil)
		return true
	}
	if m := intfP70.match(line); m != nil {
		setStrs(s.cur().sub("lapb"), m, "iframes", "rnrs", "rejs", "sabm_es", "frmrs", "discs")
		return true
	}
	if m := intfP71.match(line); m != nil {
		setStrs(s.cur().sub("serial_signals"), m, "rts", "cts", "dtr", "dcd", "dsr")
		return true
	}
	return false
}
