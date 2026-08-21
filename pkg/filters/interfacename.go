package filters

import "strings"

// ciscoInterfaceNames pairs every interface type this file normalizes
// with its short form, Cisco's own official IOS abbreviation. Scoped to
// Cisco IOS short/long form only, deliberately: no device type in this
// repository implements a capability for Junos or Arista EOS today (a
// gap PLAN.md's own catalog checklist names and declines to close), and
// their interface naming is not a short/long pair of the same scheme at
// all, so building a normalizer for either now would guess at a scheme
// this codebase has no device type to validate against.
var ciscoInterfaceNames = [][2]string{
	{"GigabitEthernet", "Gi"},
	{"FastEthernet", "Fa"},
	{"TenGigabitEthernet", "Te"},
	{"TwentyFiveGigE", "Twe"},
	{"FortyGigabitEthernet", "Fo"},
	{"HundredGigE", "Hu"},
	{"Ethernet", "Et"},
	{"Loopback", "Lo"},
	{"Vlan", "Vl"},
	{"Port-channel", "Po"},
	{"Serial", "Se"},
	{"Tunnel", "Tu"},
	{"Management", "Ma"},
}

// InterfaceShortForm normalizes a Cisco IOS interface name to its short
// form (e.g. "GigabitEthernet0/1" -> "Gi0/1"). The slot/port suffix
// (everything from the first digit onward) is passed through unchanged;
// only the interface type prefix, matched case-insensitively against
// ciscoInterfaceNames, is rewritten. Returns "" for an over-length
// input or a type prefix this file does not recognize; an
// already-short name (e.g. "Gi0/1") is returned unchanged, since its
// short form is itself.
func InterfaceShortForm(name string) string {
	if len(name) > MaxInputBytes {
		return ""
	}
	typePart, rest := splitInterfaceName(name)
	for _, pair := range ciscoInterfaceNames {
		if strings.EqualFold(typePart, pair[0]) {
			return pair[1] + rest
		}
		if strings.EqualFold(typePart, pair[1]) {
			return pair[1] + rest
		}
	}
	return ""
}

// InterfaceLongForm normalizes a Cisco IOS interface name to its long
// form (e.g. "Gi0/1" -> "GigabitEthernet0/1"), the inverse of
// InterfaceShortForm. Returns "" under the same conditions.
func InterfaceLongForm(name string) string {
	if len(name) > MaxInputBytes {
		return ""
	}
	typePart, rest := splitInterfaceName(name)
	for _, pair := range ciscoInterfaceNames {
		if strings.EqualFold(typePart, pair[0]) {
			return pair[0] + rest
		}
		if strings.EqualFold(typePart, pair[1]) {
			return pair[0] + rest
		}
	}
	return ""
}

// splitInterfaceName splits name at its first ASCII digit, the
// boundary between a Cisco interface's type name (always alphabetic,
// plus one embedded hyphen for "Port-channel") and its slot/port suffix
// (always numeric, possibly with embedded slashes, e.g. "0/0/1").
func splitInterfaceName(name string) (typePart, rest string) {
	for i := 0; i < len(name); i++ {
		if name[i] >= '0' && name[i] <= '9' {
			return name[:i], name[i:]
		}
	}
	return name, ""
}
