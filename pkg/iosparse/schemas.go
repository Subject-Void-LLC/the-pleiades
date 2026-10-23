// Ported from genieparser's src/genie/libs/parser/iosxe/show_platform.py,
// src/genie/libs/parser/iosxe/show_interface.py,
// src/genie/libs/parser/iosxe/show_routing.py,
// src/genie/libs/parser/iosxe/show_ospf.py,
// src/genie/libs/parser/iosxe/show_bgp.py,
// src/genie/libs/parser/iosxe/show_vlan.py,
// src/genie/libs/parser/iosxe/show_cdp.py,
// src/genie/libs/parser/iosxe/show_lldp.py,
// src/genie/libs/parser/iosxe/show_archive.py (commit e62f9467), Copyright
// (c) 2021 Cisco Systems, Inc. and/or its affiliates, Apache License 2.0;
// changed: translated from Python to Go. See NOTICE.genieparser.

// genieparser's schemas for the ported parsers, transcribed from each
// class's schema attribute.
package iosparse

// sch builds a schema dict from fields and slices of fields.
func sch(parts ...any) schema {
	var s schema
	for _, p := range parts {
		switch t := p.(type) {
		case field:
			s = append(s, t)
		case []field:
			s = append(s, t...)
		}
	}
	return s
}

// opts is Optional(key): k for each key.
func opts(k kind, keys ...string) []field {
	out := make([]field, 0, len(keys))
	for _, key := range keys {
		out = append(out, opt(key, k))
	}
	return out
}

// schemaShowVersion is ShowVersionSchema.
var schemaShowVersion = sch(req("version", sch(
	opt("xe_version", kStr), req("version_short", kStr), req("platform", kStr), req("version", kStr),
	opts(kStr, "label", "build_label"), req("image_id", kStr), opt("rom", orKinds{kStr, kNone}),
	req("image_type", kStr), opts(kStr, "installation_mode", "bootldr"), req("hostname", kStr), req("uptime", kStr),
	opts(kStr, "uptime_this_cp", "jawa_revision", "snowtrooper_revision"), opt("running_default_software", kBool),
	opts(kStr, "processor_board_flash", "last_reload_type", "returned_to_rom_by", "returned_to_rom_at",
		"compiled_date", "location", "copyright_years", "sp_by", "compiled_by", "system_restarted_at",
		"system_image", "last_reload_reason", "license_type", "license_level", "next_reload_license_level",
		"air_license_level", "next_reload_air_license_level", "chassis", "processor_type", "chassis_sn",
		"rtr_type", "router_operating_mode"),
	req("os", kStr), opt("curr_config_register", kStr),
	opt("license_udi", sch(opt("device_num", sch(anyKey(sch(req("pid", kStr), req("sn", kStr))))))),
	opts(kStr, "next_config_register", "main_mem"),
	opt("number_of_intfs", sch(anyKey(kStr))), opt("mem_size", sch(anyKey(kStr))),
	opt("disks", sch(anyKey(sch(opts(kStr, "disk_size", "type_of_disk"))))),
	opt("switch_num", sch(anyKey(sch(opts(kStr, "uptime", "mac_address", "mb_assembly_num",
		"power_supply_part_nr", "mb_sn", "power_supply_sn", "model_rev_num", "mb_rev_num", "model_num",
		"db_assembly_num", "db_sn", "system_sn", "top_assembly_part_num", "top_assembly_rev_num",
		"system_fpga_version", "version_id", "clei_code_num", "db_rev_num", "hb_rev_num", "mode", "model",
		"sw_image", "ports", "sw_ver"), opt("active", kBool))))),
	opt("processor", sch(opts(kStr, "cpu_type", "speed", "core", "l2_cache", "supervisor"))),
	opt("license_package", sch(anyKey(sch(req("license_level", kStr), req("license_type", kStr),
		req("next_reload_license_level", kStr))))),
	opt("module", sch(anyKey(sch(anyKey(sch(opts(kStr, "suite", "suite_current", "type", "suite_next_reboot"))))))),
	opt("image", sch(req("text_base", kStr), req("data_base", kStr))),
	opt("interfaces", sch(opts(kInt, "virtual_ethernet", "fastethernet", "gigabit_ethernet"))),
	opt("revision", sch(anyKey(kInt))),
)))

// inventoryItem is the name, descr, pid, vid and sn every inventory entry
// may carry.
var inventoryItem = opts(kStr, "name", "descr", "pid", "vid", "sn")

// inventorySlotItem is an inventory entry in a slot, which may also carry
// stack power and subslots.
var inventorySlotItem = sch(inventoryItem, opts(kStr, "swstack_power", "swstack_power_sn"),
	opt("subslot", sch(anyKey(sch(anyKey(sch(inventoryItem)))))))

// schemaShowInventory is iosxe ShowInventorySchema.
var schemaShowInventory = sch(
	opt("main", sch(opt("swstack", kBool), anyKey(sch(anyKey(sch(inventoryItem)))))),
	opt("slot", sch(anyKey(sch(
		opt("rp", sch(anyKey(inventorySlotItem))),
		opt("lc", sch(anyKey(inventorySlotItem))),
		opt("other", sch(anyKey(inventorySlotItem))),
	)))),
)

// briefFields are the columns of one show ip interface brief row.
var briefFields = sch(opts(kStr, "interface_is_ok", "method", "status", "protocol"))

// schemaShowIpInterfaceBrief is ShowIpInterfaceBriefSchema.
var schemaShowIpInterfaceBrief = sch(req("interface", sch(anyKey(sch(
	opt("vlan_id", sch(anyKey(sch(req("ip_address", kStr), briefFields)))),
	opt("ip_address", kStr), briefFields,
)))))

// schemaShowIpOspfNeighbor is ShowIpOspfNeighborSchema.
var schemaShowIpOspfNeighbor = sch(req("interfaces", sch(anyKey(sch(req("neighbors", sch(anyKey(sch(
	req("priority", kInt), req("state", kStr), req("dead_time", kStr), req("address", kStr),
)))))))))

// schemaShowCdpNeighbors is ShowCdpNeighborsSchema.
var schemaShowCdpNeighbors = sch(req("cdp", sch(
	opt("index", sch(anyKey(sch(opts(kStr, "device_id", "local_interface"), opt("hold_time", kInt),
		opts(kStr, "capability", "platform", "port_id"))))),
	req("total_entries", kInt),
)))

// schemaShowLldpNeighbors is ShowLldpNeighborsSchema.
var schemaShowLldpNeighbors = sch(
	req("total_entries", kInt),
	opt("interfaces", sch(anyKey(sch(req("port_id", sch(anyKey(lldpPort))))))),
)

// lldpPort is one remote port and the neighbors heard on it.
var lldpPort = sch(req("neighbors", sch(anyKey(sch(req("hold_time", kInt), opt("capabilities", kList))))))

// schemaShowArchiveConfigDifferences is ShowArchiveConfigDifferencesSchema.
var schemaShowArchiveConfigDifferences = sch(opt("diff", kList), opt("list_of_commands", kList))

// schemaShowVlan is ShowVlanSchema.
var schemaShowVlan = sch(req("vlans", sch(anyKey(sch(
	opts(kStr, "vlan_id", "name", "state"), opt("shutdown", kBool), opt("interfaces", kList),
	opt("type", kStr), opts(kInt, "said", "mtu"), opts(kStr, "parent", "ring_no", "bridge_no", "stp", "bridge_mode"),
	opts(kInt, "trans1", "trans2"), opt("remote_span_vlan", kBool),
	opt("private_vlan", sch(opt("primary", kBool), opt("association", kList), opt("type", kStr), opt("ports", kList))),
	opt("token_ring", sch(opts(kInt, "are_hops", "ste_hops"), opt("backup_crf", kStr))),
)))))

// bgpEntries is a total and the memory it uses.
var bgpEntries = sch(req("total_entries", kInt), req("memory_usage", kInt))

// bgpNeighborFamily is one neighbor's summary in one address family.
var bgpNeighborFamily = sch(
	req("version", kInt), req("as", orKinds{kInt, kStr}), req("msg_rcvd", kInt), req("msg_sent", kInt),
	req("tbl_ver", kInt), req("input_queue", kInt), req("output_queue", kInt), req("up_down", kStr),
	req("state_pfxrcd", kStr), opt("route_identifier", kStr), opt("local_as", orKinds{kInt, kStr}),
	opts(kInt, "bgp_table_version", "routing_table_version"),
	opt("prefixes", bgpEntries), opt("path", bgpEntries),
	opt("cache_entries", sch(anyKey(bgpEntries))), opt("entries", sch(anyKey(bgpEntries))),
	opt("community_entries", bgpEntries), opt("attribute_entries", kStr), opt("total_memory", kInt),
	opts(kStr, "activity_prefixes", "activity_paths"), opt("scan_interval", kInt),
)

// bgpNeighbor is one neighbor, by address family.
var bgpNeighbor = sch(req("address_family", sch(anyKey(bgpNeighborFamily))))

// schemaShowBgpSummary is ShowBgpSummarySchema, shared by every BGP
// summary parser.
var schemaShowBgpSummary = sch(
	req("bgp_id", orKinds{kInt, kStr}),
	req("vrf", sch(anyKey(sch(opt("neighbor", sch(anyKey(bgpNeighbor))))))),
)

// routeNextHop is one entry of a route's next_hop_list.
var routeNextHop = sch(opt("index", kInt), opts(kStr, "next_hop", "outgoing_interface", "updated", "age",
	"from", "metric", "share_count", "loading", "hops", "minimum_mtu", "reliability", "minimum_bandwidth",
	"total_delay", "vrf"))

// schemaShowIpRoute is ShowIpRouteSchema.
var schemaShowIpRoute = sch(req("vrf", sch(anyKey(sch(opt("address_family", sch(anyKey(sch(
	opt("routes", sch(anyKey(sch(
		opt("route", kStr), opt("active", kBool), opts(kInt, "route_preference", "metric"),
		opts(kStr, "source_protocol", "source_protocol_codes", "mask", "known_via"), opt("distance", kInt),
		opts(kStr, "type", "net", "redist_via", "redist_via_tag"),
		opt("update", sch(req("from", kStr), req("interface", kStr), req("age", kStr))),
		opt("next_hop", sch(
			opt("outgoing_interface", sch(anyKey(sch(opts(kStr, "outgoing_interface", "updated", "vrf"))))),
			opt("next_hop_list", sch(anyKey(routeNextHop))),
		)),
	)))),
)))))))))
