// Ported from genieparser's src/genie/libs/parser/iosxe/show_interface.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// genieparser's ShowInterfacesSchema, transcribed.
package iosparse

// intfCounterKeys are the int counters an interface's counters may hold.
var intfCounterKeys = []string{"in_multicast_pkts", "in_broadcast_pkts", "in_crc_errors", "in_giants", "in_pkts",
	"in_frame", "in_runts", "in_overrun", "in_ignored", "in_watchdog", "in_with_dribble", "in_octets", "in_errors",
	"in_abort", "in_no_buffer", "in_throttles", "in_mac_pause_frames", "in_drops", "out_pkts", "out_octets",
	"out_multicast_pkts", "out_broadcast_pkts", "out_errors", "out_collision", "out_interface_resets",
	"out_unknown_protocl_drops", "out_babbles", "out_deferred", "out_underruns", "out_late_collision",
	"out_lost_carrier", "out_no_carrier", "out_babble", "out_mac_pause_frames", "out_buffer_failure",
	"out_buffers_swapped", "out_drops"}

// intfCounters is an interface's counters.
var intfCounters = sch(
	opt("rate", sch(opts(kInt, "load_interval", "in_rate", "in_rate_pkts", "out_rate", "out_rate_pkts",
		"in_rate_bps", "in_rate_pps", "out_rate_bps", "out_rate_pps"))),
	opts(kInt, intfCounterKeys...),
	opt("last_clear", kStr),
)

// intfX25 is an interface's X.25 state.
var intfX25 = sch(
	opt("dte", kBool), opts(kStr, "version", "address", "state"), opts(kInt, "modulo", "timer", "idle_vc_timeout"),
	opt("encapsulation", kStr),
	opts(kInt, "input_window_size", "output_window_size", "input_packet_size", "output_packet_size"),
	opt("timers", sch(opts(kInt, "t20", "t21", "t22", "t23"))),
	opt("channels", sch(opts(kStr, "incoming_only", "two_way", "outgoing_only"))),
	opts(kStr, "restarts", "calls", "diags"),
)

// intfLAPB is an interface's LAPB state.
var intfLAPB = sch(
	opt("dte", kBool), opt("state", kStr),
	opts(kInt, "modulo", "k", "n1", "n2", "t1", "t2", "t3", "t4", "vs", "vr", "tx_nr", "remote_vr", "retransmissions"),
	opt("queues", sch(opts(kInt, "u_s_frames", "i_frames", "unack", "retx"))),
	opts(kStr, "iframes", "rnrs", "rejs", "sabm_es", "frmrs", "discs"),
)

// schemaShowInterfaces is ShowInterfacesSchema.
var schemaShowInterfaces = sch(anyKey(sch(
	opts(kStr, "oper_status", "line_protocol"),
	opts(kBool, "enabled", "is_deleted", "is_present", "connected", "err_disabled", "suspended"),
	opts(kStr, "description", "type", "link_state", "port_speed", "duplex_mode", "link_type", "media_type"),
	opt("mtu", kInt),
	opts(kStr, "maximum_active_vcs", "vcs_per_vp", "vc_idle_disconnect_time", "vc_auto_creation", "current_vccs"),
	opts(kInt, "aal5_crc_errors", "aal5_oversized_sdus", "aal5_sar_timeouts"),
	opts(kStr, "vaccess_status", "vaccess_loopback", "base_pppoatm", "dtr_pulsed"),
	opt("sub_mtu", kInt),
	opts(kStr, "medium", "reliability", "txload", "rxload", "mac_address", "phys_address"),
	opts(kInt, "delay", "carrier_delay", "carrier_delay_up", "carrier_delay_down", "keepalive"),
	opt("auto_negotiate", kBool),
	opts(kStr, "arp_type", "arp_timeout", "last_input", "last_output", "output_hang"),
	opt("autostate", kBool),
	opts(kStr, "tunnel_source_ip", "tunnel_source_interface", "tunnel_destination_ip", "tunnel_protocol"),
	opts(kInt, "tunnel_ttl", "tunnel_transport_mtu", "tunnel_transmit_bandwidth", "tunnel_receive_bandwidth"),
	opts(kStr, "tunnel_protection", "tunnel_profile"),
	opt("carrier_transitions", kInt),
	opt("queues", sch(opts(kInt, "input_queue_size", "input_queue_max", "input_queue_drops", "input_queue_flushes",
		"total_output_drop"), opt("queue_strategy", kStr),
		opts(kInt, "output_queue_size", "output_queue_max", "threshold", "drops"))),
	opt("flow_control", sch(opts(kBool, "receive", "send"))),
	opt("port_channel", sch(opt("port_channel_member", kBool), opt("port_channel_int", kStr),
		opt("port_channel_member_intfs", kList), opts(kInt, "active_members", "num_of_pf_jumbo_supported_members"))),
	opt("bandwidth", orKinds{kInt, kStr}),
	opt("counters", intfCounters),
	opt("encapsulations", sch(opts(kStr, "encapsulation", "first_dot1q", "second_dot1q"), opt("native_vlan", kInt))),
	opt("ipv4", sch(
		anyKey(sch(opts(kStr, "ip", "prefix_length"), opt("secondary", kBool))),
		opt("unnumbered", sch(req("interface_ref", kStr))),
	)),
	opt("peer_ip", kStr), opt("vc_id", kInt),
	opt("x25", intfX25),
	opt("lapb", intfLAPB),
	opt("serial_signals", sch(opts(kStr, "rts", "cts", "dtr", "dcd", "dsr"))),
)))
