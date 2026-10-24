// Ported from genieparser's src/genie/libs/parser/iosxe/show_platform.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowVersion: genieparser iosxe/show_platform.py's ShowVersion.cli, for
// "show version".
package iosparse

// ShowVersion's expressions, named as genieparser names them.
var (
	verP0   = re(`^Cisco +([\S\s]+) +Software, +Version +(?P<xe_version>.*)$`)
	verP1   = re(`^[Cc]isco +IOS +[Ss]oftware\, +(?P<os>([\S]+)) +Software\, ` + `+(?P<platform>.+) Software +\((?P<image_id>.+)\).+[Vv]ersion ` + `+(?P<version>\S+) +.*$`)
	verP1_1 = re(`^(?P<os>[A-Z]+) +\(.*\) +(?P<platform>.+) +Software` + ` +\((?P<image_id>.+)\).+( +Experimental)? +[Vv]ersion` + ` +(?P<version>\S+), +(EARLY DEPLOYMENT |PLATFORM SPECIFIC |SHARED PLATFORM, )?RELEASE SOFTWARE .*$`)
	verP2   = re(`^(?P<ver_short>\d+\.\d+).*`)
	verP3   = re(`^[Cc]isco +(?P<os>[A-Z]+) +[Ss]oftware\s*\[?(?P<location>\w*)?\]?\, ` + `+(?P<platform>.+) +Software +\((?P<image_id>.+)\).+( ` + `+Experimental)? +[Vv]ersion ` + `+(?P<version>[\w.:()]+) *,? *` + `(?P<label>(\[.*?(?P<build_label>BLD_\w+)([-:]\S+)? \d+\])|.*)$`)
	verP4   = re(`^Copyright +\(c\) +(?P<copyright_years>\d+-\d+).*$`)
	verP5   = re(`^Technical +Support: +http\:\/\/www` + `\.cisco\.com\/techsupport`)
	verP6   = re(`^ROM\:( +(?P<rom>.+?)(?:BOOTLDR\: +(?P<bootldr>.+))?$)?`)
	verP7   = re(`^Bootstrap +program +is +(?P<os>.+)$`)
	verP8   = re(`^BOOTLDR\: +(?P<bootldr>.+)$`)
	verP9   = re(`^(?P<hostname>.+) +uptime +is +(?P<uptime>.+)$`)
	verP10  = re(`^[Uu]ptime +for +this +control +processor ` + `+is +(?P<uptime_this_cp>.+)$`)
	verP11  = re(`^[Ss]ystem +restarted +at ` + `+(?P<system_restarted_at>.+)$`)
	verP12  = re(`^[Ss]ystem +image +file +is ` + `+\"(?P<system_image>.+)\"`)
	verP13  = re(`^[Ll]ast +reload +reason\: ` + `+(?P<last_reload_reason>.+)$`)
	verP14  = re(`^[Ll]ast +reset +from +(?P<last_reload_reason>.+)$`)
	verP15  = re(`^[Ll]icense +[Tt]ype\: +(?P<license_type>.+)$`)
	verP16  = re(`^\s*[Ll]icense +[Ll]evel\: +(?P<license_level>.+)$`)
	verP161 = re(`(?P<license_level>\S+) +Type\: +(?P<license_type>.+)$`)
	verP162 = re(`^\s*AIR [Ll]icense +[Ll]evel\: +(?P<air_license_level>.+)$`)
	verP163 = re(`^Current  +Type  +Next reboot`)
	verP164 = re(`^(?P<license_package>[\w-]+)(?:\s{2,})(?P<package_license_type>(\w+ )+)(?:\s{2,})(?P<next_reload_license_level>\S+)\s*$`)
	verP17  = re(`^[Nn]ext +(reload|reboot) +license +Level\: ` + `+(?P<next_reload_license_level>.+)$`)
	verP171 = re(`^[Nn]ext +(reload|reboot) +AIR license +Level\: ` + `+(?P<next_reload_air_license_level>.+)$`)
	verP18  = re(`^(C|c)isco +(?P<chassis>[a-zA-Z0-9\-\/\+]+) ` + `+\((?P<processor_type>[^)]*)\) +(.*?)with ` + `+(?P<main_mem>[0-9]+)[kK](\/[0-9]+[kK])?`)
	verP182 = re(`^(C|c)isco +(?P<chassis>[a-zA-Z0-9\-\/\+]+) +.* ` + `+with +(?P<processor_type>.+) +with +(?P<main_mem>[0-9]+)[kK](\/[0-9]+[kK])?`)
	verP183 = re(`^(A|a)llen-Bradley +(?P<chassis>[a-zA-Z0-9\-\/\+]+) ` + `+\((?P<processor_type>[^)]*)\) +(.*?)with ` + `+(?P<main_mem>[0-9]+)[kK](\/[0-9]+[kK])?`)
	verP19  = re(`^[pP]rocessor +board +ID ` + `+(?P<chassis_sn>[a-zA-Z0-9]+)`)
	verP20  = re(`^(?P<number_of_ports>\d+) +(?P<interface>.+) ` + `+(interface(?:s)?|line|port(?:s)?)$`)
	verP21  = re(`^(?P<mem_size>\d+)K +bytes +of ` + `+(?P<memories>.+) +[Mm]emory\.`)
	verP22  = re(`^(?P<disk_size>\d+)K bytes of ` + `(?P<type_of_disk>.*) at (?P<disks>.+)$`)
	verP23  = re(`^[Cc]isco +(?P<os>[a-zA-Z\-]+) ` + `+[Ss]oftware\,`)
	verP24  = re(`^[Cc]onfiguration +register +is ` + `+(?P<curr_config_register>[a-zA-Z0-9]+)`)
	verP25  = re(`^[Cc]onfiguration +register +is +[a-zA-Z0-9]+ ` + `+\(will be (?P<next_config_register>[a-zA-Z0-9]+) ` + `at next reload\)`)
	verP26  = re(`^[Ss]witch +0(?P<switch_number>\d+)$`)
	verP27  = re(`^[Ss]witch +[Uu]ptime +\: +(?P<uptime>.+)$`)
	verP36  = re(`^Compiled +(?P<compiled_date>[\S\s]+) +by ` + `+(?P<compiled_by>\w+)$`)
	verP37  = re(`^System +returned +to +ROM +by ` + `+(?P<returned_to_rom_by>[\w\s\-]+)(?: +at ` + `+(?P<returned_to_rom_at>[\w\s\:]+))?(?: +\(SP +by ` + `+(?P<sp_by>[\S\s\-]+)\))?$`)
	verP38  = re(`^Last +reload +type\: +(?P<last_reload_type>[\S ]+)$`)
	verP39  = re(`^(?P<cpu_name>\S+) +(CPU|cpu|Cpu) +at ` + `+(?P<speed>\S+)\,(( +(?P<core>\S+) +core\, ` + `+(?P<l2_cache>\S+) +L2 +[Cc]ache)|( +Supervisor ` + `+(?P<supervisor>\S+)))$`)
	verP40  = re(`^(?P<processor_board_flash>\S+) +bytes .+$`)
	verP41  = re(`^Running +(?P<running_default_software>\S+) +software$`)
	verP42  = re(`^Jawa +Revision +(?P<jawa_revision>\S+)\, ` + `+Snowtrooper +Revision +(?P<snowtrooper_rev>\S+)$`)
	verP43  = re(`^(?P<technology>\w[\w\-]+)(?: {2,}` + `(?P<license_level>\w+) {2,}(?P<license_type>\w+(?: ` + `+\w+)?) {2,}(?P<next_boot>\w+))?$`)
	verP44  = re(`^(?P<aname>Suite|Technology) +((Suite +Current)|` + `(Technology\-package))`)
	verP45  = re(`^[Ss]uite +[Ll]icense +[Ii]nformation +for ` + `+[Mm]odule\:\'(?P<module>\S+)\'$`)
	verP460 = re(`^License UDI:$`)
	verP46  = re(`^(?P<device_num>[*\d]+) +(?P<pid>[\S]+) +(?P<sn>[A-Z\d]+)$`)
	verP47  = re(`^Image text-base: +(?P<text_base>\S+), ` + `data-base: +(?P<data_base>\S+)$`)
	verP48  = re(`^(?P<interface>\d+) +(?P<ethernet_type>Virtual Ethernet|Gigabit Ethernet|FastEthernet)` + `/IEEE 802\.3 +interface\(s\)$`)
	verP50  = re(`^(?P<group1>\S+)\s+Revision\s+(?P<group1_int>\d+),` + `\s+(?P<group2>\S+)\s+Revision\s+(?P<group2_int>\d+)$`)
	verP61  = re(`^Router operating mode: (?P<router_operating_mode>.+)$`)
	verP62  = re(`^Installation\s+mode\s+is\s+(?P<installation_mode>.+)$`)
	verP63  = re(`^System FPGA version\s+:\s+(?P<system_fpga_version>(\d+\.?)+)`)
)

// verSwitchField is one of the per-switch lines ShowVersion records on the
// switch being described, or on the active switch when no "Switch 0N"
// heading has appeared yet.
type verSwitchField struct {
	// re matches the line.
	re *rx
	// key is both the result key and the expression's group name.
	key string
}

// verSwitchFields are ShowVersion's p28 to p34 and p51 to p60, in the
// order genieparser tries them.
var verSwitchFields = []verSwitchField{
	{re(`^[Bb]ase +[Ee]thernet +MAC +[Aa]ddress ` + `+\: +(?P<mac_address>.+)$`), "mac_address"},
	{re(`^[Mm]otherboard +[Aa]ssembly +[Nn]umber +\: ` + `+(?P<mb_assembly_num>.+)$`), "mb_assembly_num"},
	{re(`^[Mm]otherboard +[Ss]erial +[Nn]umber +\: ` + `+(?P<mb_sn>.+)$`), "mb_sn"},
	{re(`^[Mm]odel +[Rr]evision +[Nn]umber +\: ` + `+(?P<model_rev_num>.+)$`), "model_rev_num"},
	{re(`^[Mm]otherboard +[Rr]evision +[Nn]umber +\: ` + `+(?P<mb_rev_num>.+)$`), "mb_rev_num"},
	{re(`^[Mm]odel +[Nn]umber +\: +(?P<model_num>.+)$`), "model_num"},
	{re(`^[Ss]ystem +[Ss]erial +[Nn]umber +\: +(?P<system_sn>.+)$`), "system_sn"},
	{re(`^[Pp]ower\s+[Ss]upply\s+[Pp]art\s+[Nn]umber\s+\:\s+(?P<power_supply_part_nr>.+)$`), "power_supply_part_nr"},
	{re(`^[Pp]ower\s+[Ss]upply\s+[Ss]erial\s+[Nn]umber\s+\:\s+(?P<power_supply_sn>.+)$`), "power_supply_sn"},
	{re(`^[Dd]aughterboard\s+[Aa]ssembly\s+[Nn]umber\s+\:\s+(?P<db_assembly_num>.+)$`), "db_assembly_num"},
	{re(`^[Dd]aughterboard\s+[Ss]erial\s+[Nn]umber\s+\:\s+(?P<db_sn>.+)$`), "db_sn"},
	{re(`^[Tt]op\s+[Aa]ssembly\s+[Pp]art\s+[Nn]umber\s+\:\s+(?P<top_assembly_part_num>.+)$`), "top_assembly_part_num"},
	{re(`^[Tt]op\s+[Aa]ssembly\s+[Rr]evision\s+[Nn]umber\s+\:\s+(?P<top_assembly_rev_num>.+)$`), "top_assembly_rev_num"},
	{re(`^[Vv]ersion\s+ID\s+\:\s+(?P<version_id>.+)$`), "version_id"},
	{re(`^CLEI\s+[Cc]ode\s+[Nn]umber\s+\:\s+(?P<clei_code_num>.+)$`), "clei_code_num"},
	{re(`^[Dd]aughterboard\s+[Rr]evision\s+[Nn]umber\s+\:\s+(?P<db_rev_num>.+)$`), "db_rev_num"},
	{re(`^[Hh]ardware\s+[Bb]oard\s+[Rr]evision\s+[Nn]umber\s+\:\s+(?P<hb_rev_num>.+)$`), "hb_rev_num"},
}

// verState is ShowVersion.cli's local variables. A Python local that may
// be read before it is assigned is held with a flag, so reading it then
// raises NameError as Python does.
type verState struct {
	// out is version_dict, the result.
	out *dict
	// active is active_dict: per-switch lines seen before any "Switch 0N".
	active *dict
	// suiteFlag and licenseFlag say which license table is being read.
	suiteFlag, licenseFlag bool
	// switchNumber is the last "Switch 0N" number, when one has been seen.
	switchNumber string
	hasSwitch    bool
	// uptimeThisCP is the control processor uptime, when one has been seen.
	uptimeThisCP string
	hasUptimeCP  bool
	// suite is suite_dict, the module whose suite licenses are being read.
	suite *dict
	// licenseUDI is license_udi_dict.
	licenseUDI *dict
}

// ver is version_dict['version'], raising KeyError before it exists.
func (s *verState) ver() *dict {
	return s.out.d("version")
}

// switchDict is version_dict['version']['switch_num'][switch_number].
func (s *verState) switchDict() *dict {
	if !s.hasSwitch {
		raise("NameError", "switch_number")
	}
	return s.ver().d("switch_num").d(s.switchNumber)
}

// parseShowVersion is ShowVersion.cli.
func parseShowVersion(out string, _ map[string]string) *dict {
	s := &verState{out: newDict(), active: newDict()}
	for _, raw := range splitlines(out) {
		s.line(strip(raw))
	}
	s.switchTable(out)
	s.licenseCompat()
	return s.out
}

// line handles one stripped line of output, in genieparser's order.
func (s *verState) line(line string) {
	// Each group reports whether it handled the line, Python's continue;
	// the first that does ends the line.
	_ = s.versionLines(line) || s.systemLines(line) || s.licenseLines(line) ||
		s.hardwareLines(line) || s.switchLines(line) || s.moduleLines(line)
}

// versionLines handles p0 to p8: the software version, copyright and ROM.
func (s *verState) versionLines(line string) bool {
	if m := verP0.match(line); m != nil {
		s.out.sub("version").set("xe_version", m.g("xe_version"))
		return true
	}
	m := verP1.match(line)
	if m == nil {
		m = verP1_1.match(line)
	}
	if m != nil {
		if m2 := verP2.match(m.g("version")); m2 != nil {
			v := s.out.sub("version")
			v.set("version_short", m2.g("ver_short"))
			v.set("platform", strip(m.g("platform")))
			v.set("version", m.g("version"))
			v.set("image_id", m.g("image_id"))
			v.set("os", m.g("os"))
			return true
		}
	}
	if m := verP3.match(line); m != nil {
		if m2 := verP2.match(m.g("version")); m2 != nil {
			v := s.out.sub("version")
			v.set("version_short", m2.g("ver_short"))
			v.set("platform", m.g("platform"))
			v.set("version", m.g("version"))
			v.set("image_id", m.g("image_id"))
			for _, k := range []string{"label", "build_label", "os", "location"} {
				if m.g(k) != "" {
					v.set(k, m.g(k))
				}
			}
			return true
		}
	}
	if m := verP4.match(line); m != nil {
		s.out.sub("version").setdefault("image_type", "developer image")
		s.out.sub("version").setdefault("copyright_years", m.g("copyright_years"))
		return true
	}
	if verP5.match(line) != nil {
		s.out.sub("version").setdefault("image_type", "production image")
		return true
	}
	if m := verP6.match(line); m != nil {
		rom := m.v("rom")
		s.ver().set("rom", rom)
		if m.g("bootldr") != "" {
			s.ver().set("bootldr", m.g("bootldr"))
		}
		if rom != nil {
			if m7 := verP7.match(rom.(string)); m7 != nil && !s.ver().has("os") {
				s.ver().set("os", m7.g("os"))
			}
		}
		return true
	}
	if m := verP8.match(line); m != nil {
		s.ver().set("bootldr", m.g("bootldr"))
		return true
	}
	return false
}

// systemLines handles p9 to p15: hostname, uptime, image and reload.
func (s *verState) systemLines(line string) bool {
	if m := verP9.match(line); m != nil {
		s.ver().set("hostname", m.g("hostname"))
		s.ver().set("uptime", m.g("uptime"))
		return true
	}
	if m := verP10.match(line); m != nil {
		s.ver().set("uptime_this_cp", m.g("uptime_this_cp"))
		s.uptimeThisCP, s.hasUptimeCP = m.g("uptime_this_cp"), true
		return true
	}
	singles := []struct {
		re  *rx
		key string
	}{
		{verP11, "system_restarted_at"},
		{verP12, "system_image"},
		{verP13, "last_reload_reason"},
		{verP14, "last_reload_reason"},
		{verP15, "license_type"},
	}
	for _, p := range singles {
		if m := p.re.match(line); m != nil {
			s.ver().set(p.key, m.g(p.key))
			return true
		}
	}
	return false
}

// licenseLines handles p16 to p17_1: license levels and the license
// package table. p17_1 does not end the line's handling, as in Python.
func (s *verState) licenseLines(line string) bool {
	if m := verP16.match(line); m != nil {
		level := m.g("license_level")
		if contains(level, "Type:") {
			if m1 := verP161.match(strip(level)); m1 != nil {
				s.ver().set("license_type", m1.g("license_type"))
				s.ver().set("license_level", m1.g("license_level"))
			}
		} else {
			s.ver().set("license_level", level)
		}
		return true
	}
	if m := verP162.match(line); m != nil {
		s.ver().set("air_license_level", m.g("air_license_level"))
		return true
	}
	if verP163.match(line) != nil {
		s.ver().setdefault("license_package", newDict())
		return true
	}
	if m := verP164.match(line); m != nil {
		pkg := m.g("license_package")
		s.ver().setdefault("license_package", newDict())
		entry := newDict()
		entry.set("license_level", pkg)
		entry.set("license_type", strip(m.g("package_license_type")))
		entry.set("next_reload_license_level", m.g("next_reload_license_level"))
		s.ver().d("license_package").set(pkg, entry)
		return true
	}
	if m := verP17.match(line); m != nil {
		s.ver().set("next_reload_license_level", m.g("next_reload_license_level"))
		return true
	}
	if m := verP171.match(line); m != nil {
		s.ver().set("next_reload_air_license_level", m.g("next_reload_air_license_level"))
	}
	return false
}
