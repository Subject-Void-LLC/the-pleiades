// Ported from genieparser's src/genie/libs/parser/iosxe/show_platform.py
// (commit e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its
// affiliates, Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// ShowVersion, continued: hardware, switch stack, module and license
// lines, the switch table and the license compatibility step.
package iosparse

import (
	"strconv"
	"strings"
)

// hardwareLines handles p18 to p25: chassis, memory, interfaces, disks
// and the configuration register. p24 does not end the line's handling,
// as in Python.
func (s *verState) hardwareLines(line string) bool {
	m := verP18.match(line)
	if m == nil {
		m = verP182.match(line)
	}
	if m == nil {
		m = verP183.match(line)
	}
	if m != nil {
		v := s.ver()
		chassis := m.g("chassis")
		v.set("chassis", chassis)
		v.set("main_mem", m.g("main_mem"))
		v.set("processor_type", m.g("processor_type"))
		switch {
		case contains(chassis, "C3850") || contains(chassis, "C3650"):
			v.set("rtr_type", "Edison")
		case contains(chassis, "ASR1"):
			v.set("rtr_type", "ASR1K")
		case contains(chassis, "CSR1000V"):
			v.set("rtr_type", "CSR1000V")
		case contains(chassis, "C11"):
			v.set("rtr_type", "ISR")
		default:
			v.set("rtr_type", chassis)
		}
		return true
	}
	if m := verP61.match(line); m != nil {
		s.ver().set("router_operating_mode", m.g("router_operating_mode"))
		return true
	}
	if m := verP62.match(line); m != nil {
		s.ver().set("installation_mode", m.g("installation_mode"))
		return true
	}
	if m := verP19.match(line); m != nil {
		s.ver().set("chassis_sn", m.g("chassis_sn"))
		return true
	}
	if m := verP20.match(line); m != nil {
		s.ver().sub("number_of_intfs").set(m.g("interface"), m.g("number_of_ports"))
		return true
	}
	if m := verP21.match(line); m != nil {
		s.ver().sub("mem_size").set(m.g("memories"), m.g("mem_size"))
		return true
	}
	if m := verP22.match(line); m != nil {
		disk := s.ver().sub("disks").sub(m.g("disks"))
		disk.set("disk_size", m.g("disk_size"))
		disk.set("type_of_disk", m.g("type_of_disk"))
		return true
	}
	if m := verP23.match(line); m != nil {
		s.ver().set("os", m.g("os"))
		return true
	}
	if m := verP24.match(line); m != nil {
		s.ver().set("curr_config_register", m.g("curr_config_register"))
	}
	if m := verP25.match(line); m != nil {
		s.ver().set("next_config_register", m.g("next_config_register"))
		return true
	}
	return false
}

// switchLines handles p26, p27 and the per-switch hardware lines.
func (s *verState) switchLines(line string) bool {
	if m := verP26.match(line); m != nil {
		s.switchNumber, s.hasSwitch = m.g("switch_number"), true
		s.ver().sub("switch_num").sub(s.switchNumber)
		return true
	}
	if m := verP27.match(line); m != nil {
		if s.ver().has("switch_num") {
			s.switchDict().set("uptime", m.g("uptime"))
		}
		return true
	}
	for _, f := range verSwitchFields {
		if m := f.re.match(line); m != nil {
			s.switchOrActive(f.key, m.g(f.key))
			return true
		}
	}
	return false
}

// switchOrActive records a per-switch value on the current switch, or on
// the active switch when no switch heading has been read.
func (s *verState) switchOrActive(key, value string) {
	if !s.ver().has("switch_num") {
		s.active.setdefault(key, value)
		return
	}
	s.switchDict().set(key, value)
}

// moduleLines handles p36 to p63: build, reload, processor, licenses by
// technology and suite, the license UDI and the remaining counters.
func (s *verState) moduleLines(line string) bool {
	if m := verP36.match(line); m != nil {
		s.ver().set("compiled_date", m.g("compiled_date"))
		s.ver().set("compiled_by", m.g("compiled_by"))
		return true
	}
	if m := verP37.match(line); m != nil {
		s.ver().set("returned_to_rom_by", m.g("returned_to_rom_by"))
		if m.g("returned_to_rom_at") != "" {
			s.ver().set("returned_to_rom_at", m.g("returned_to_rom_at"))
		}
		if m.g("sp_by") != "" {
			s.ver().set("sp_by", m.g("sp_by"))
		}
		return true
	}
	if m := verP38.match(line); m != nil {
		s.ver().set("last_reload_type", m.g("last_reload_type"))
		return true
	}
	if m := verP39.match(line); m != nil {
		cpu := s.ver().sub("processor")
		cpu.set("cpu_type", m.g("cpu_name"))
		cpu.set("speed", m.g("speed"))
		if m.g("supervisor") != "" {
			cpu.set("supervisor", m.g("supervisor"))
		} else {
			cpu.set("core", m.v("core"))
			cpu.set("l2_cache", m.v("l2_cache"))
		}
		return true
	}
	if m := verP40.match(line); m != nil {
		s.ver().set("processor_board_flash", m.g("processor_board_flash"))
		return true
	}
	if verP41.match(line) != nil {
		s.ver().set("running_default_software", true)
		return true
	}
	if m := verP42.match(line); m != nil {
		s.ver().set("jawa_revision", m.g("jawa_revision"))
		s.ver().set("snowtrooper_revision", m.g("snowtrooper_rev"))
		return true
	}
	return s.licenseTables(line) || s.tailLines(line)
}

// licenseTables handles p43 to p46: the technology and suite license
// tables and the license UDI.
func (s *verState) licenseTables(line string) bool {
	if m := verP43.match(line); m != nil {
		if s.licenseFlag {
			lic := s.ver().sub("license_package").sub(m.g("technology"))
			if m.g("license_type") != "" {
				lic.set("license_type", m.g("license_type"))
			}
			if m.g("license_level") != "" {
				lic.set("license_level", m.g("license_level"))
			}
			if m.g("next_boot") != "" {
				lic.set("next_reload_license_level", m.g("next_boot"))
			}
		}
		if s.suiteFlag {
			if s.suite == nil {
				raise("NameError", "suite_dict")
			}
			suite := s.suite.sub(m.g("technology"))
			if m.g("license_level") != "" {
				suite.set("suite_current", m.g("license_level"))
			}
			if m.g("license_type") != "" {
				suite.set("type", strip(m.g("license_type")))
			}
			if m.g("next_boot") != "" {
				suite.set("suite_next_reboot", m.g("next_boot"))
			}
		}
		return true
	}
	if m := verP44.match(line); m != nil {
		if contains(m.g("aname"), "Suite") {
			s.suiteFlag = true
		}
		if contains(m.g("aname"), "Technology") {
			s.licenseFlag = true
			s.suiteFlag = false
		}
		return true
	}
	if m := verP45.match(line); m != nil {
		s.suite = s.ver().sub("module").sub(m.g("module"))
		return true
	}
	if verP460.match(line) != nil {
		// genieparser tests version_dict, not version_dict['version'],
		// for license_udi, so the test always passes.
		s.licenseUDI = s.ver().sub("license_udi")
		return true
	}
	if m := verP46.match(line); m != nil {
		if s.licenseUDI == nil {
			raise("NameError", "license_udi_dict")
		}
		sub := s.licenseUDI.sub("device_num").sub(m.g("device_num"))
		sub.set("pid", m.g("pid"))
		sub.set("sn", m.g("sn"))
		return true
	}
	return false
}

// tailLines handles p47, p48, p50 and p63.
func (s *verState) tailLines(line string) bool {
	if m := verP47.match(line); m != nil {
		image := newDict()
		image.set("text_base", m.g("text_base"))
		image.set("data_base", m.g("data_base"))
		s.ver().set("image", image)
		return true
	}
	if m := verP48.match(line); m != nil {
		ethernetType := strings.Join(fields(strings.ToLower(m.g("ethernet_type"))), "_")
		s.ver().sub("interfaces").set(ethernetType, pyInt(m.g("interface")))
		return true
	}
	if m := verP50.match(line); m != nil {
		rev := newDict()
		s.ver().set("revision", rev)
		rev.set(m.g("group1"), pyInt(m.g("group1_int")))
		rev.set(m.g("group2"), pyInt(m.g("group2_int")))
		return true
	}
	if m := verP63.match(line); m != nil {
		s.switchOrActive("system_fpga_version", m.g("system_fpga_version"))
		return true
	}
	return false
}

// verTableXE and verTableIOS are the two shapes of the switch stack table
// ShowVersion reads, with and without the Mode column.
var (
	verTableXE = tabular{
		headers:        []string{"Switch", "Ports", "Model             ", "SW Version       ", "SW Image              ", "Mode   "},
		labels:         []string{"switch_num", "ports", "model", "sw_ver", "sw_image", "mode"},
		rightJustified: true,
		terminal:       re(`(^\n|^\s*$)`),
	}
	verTableIOS = tabular{
		headers:        []string{"Switch", "Ports", "Model             ", "SW Version       ", "SW Image              "},
		labels:         []string{"switch_num", "ports", "model", "sw_ver", "sw_image"},
		rightJustified: true,
		terminal:       re(`(^\n|^\s*$)`),
	}
	// verStarKey takes the switch number from a starred row, "*    1".
	verStarKey = re(`\**\ *(?P<new_key>\d)`)
)

// switchTable reads the switch stack table, or, when there is none,
// files the active switch's lines under the first free switch number.
func (s *verState) switchTable(out string) {
	entries := verTableXE.fill(out)
	if entries.size() == 0 {
		entries = verTableIOS.fill(out)
	}
	if entries.size() == 0 {
		if s.active.size() > 0 {
			s.fileActive()
		}
		return
	}
	for _, key := range entries.keys {
		row := entries.d(key)
		switches := s.ver().sub("switch_num")
		if contains(key, "*") {
			m := verStarKey.match(key)
			if m == nil {
				raise("AttributeError", "'NoneType' object has no attribute 'groupdict'")
			}
			sw := switches.sub(m.g("new_key"))
			for _, k := range row.keys {
				if k != "switch_num" {
					sw.set(k, row.vals[k])
				}
			}
			if s.hasUptimeCP {
				sw.set("uptime", s.uptimeThisCP)
			}
			sw.set("active", true)
			sw.update(s.active)
			continue
		}
		sw := switches.sub(key)
		for _, k := range row.keys {
			if k != "switch_num" {
				sw.set(k, row.vals[k])
			}
		}
		sw.set("active", false)
	}
}

// fileActive inserts the active switch's lines into the first switch
// number not already used.
func (s *verState) fileActive() {
	used := map[int64]bool{}
	count := 0
	if v, ok := s.out.get("version").(*dict); ok {
		if sw, ok := v.get("switch_num").(*dict); ok {
			for _, k := range sw.keys {
				used[pyInt(k)] = true
				count++
			}
		}
	}
	for num := int64(1); num < int64(count)+2; num++ {
		if !used[num] {
			s.out.sub("version").sub("switch_num").sub(strconv.FormatInt(num, 10)).update(s.active)
			return
		}
	}
}

// licenseCompat copies the one license package's level and type up to the
// version, for genieparser's backward compatibility.
func (s *verState) licenseCompat() {
	pkgs, ok := s.ver().get("license_package").(*dict)
	if !ok || pkgs.size() != 1 {
		return
	}
	info := pkgs.d(pkgs.keys[0])
	for _, k := range []string{"license_level", "license_type", "next_reload_license_level"} {
		s.ver().setdefault(k, info.get(k))
	}
}
