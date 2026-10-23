// Ported from genieparser's src/genie/libs/parser/utils/common.py (commit
// e62f9467), Copyright (c) 2021 Cisco Systems, Inc. and/or its affiliates,
// Apache License 2.0; changed: translated from Python to Go. See
// NOTICE.genieparser.

// Common.convert_intf_name from genieparser's utils/common.py.
package iosparse

import "strings"

// intfAbbreviations is genieparser's INTERFACE_ABBREVIATION_MAPPING_TABLE
// for the "generic" operating system, the one every ported parser uses:
// an interface type abbreviation and the full name IOS gives it.
var intfAbbreviations = map[string]string{
	"Eth":                 "Ethernet",
	"SEth":                "Service-Ethernet",
	"Lo":                  "Loopback",
	"lo":                  "Loopback",
	"Fa":                  "FastEthernet",
	"Fas":                 "FastEthernet",
	"Po":                  "Port-channel",
	"PO":                  "Port-channel",
	"Null":                "Null",
	"Gi":                  "GigabitEthernet",
	"Gig":                 "GigabitEthernet",
	"GE":                  "GigabitEthernet",
	"Te":                  "TenGigabitEthernet",
	"Ten":                 "TenGigabitEthernet",
	"Tw":                  "TwoGigabitEthernet",
	"Two":                 "TwoGigabitEthernet",
	"Twe":                 "TwentyFiveGigE",
	"Fi":                  "FiveGigabitEthernet",
	"Fiv":                 "FiveGigabitEthernet",
	"Fif":                 "FiftyGigE",
	"Fifty":               "FiftyGigabitEthernet",
	"mgmt":                "mgmt",
	"Vl":                  "Vlan",
	"Tu":                  "Tunnel",
	"Hs":                  "HSSI",
	"AT":                  "ATM",
	"Et":                  "Ethernet",
	"BD":                  "BDI",
	"Ser":                 "Serial",
	"Se":                  "Serial",
	"Fo":                  "FortyGigabitEthernet",
	"For":                 "FortyGigabitEthernet",
	"Hu":                  "HundredGigE",
	"Hun":                 "HundredGigE",
	"TwoH":                "TwoHundredGigabitEthernet",
	"Fou":                 "FourHundredGigE",
	"vl":                  "vasileft",
	"vr":                  "vasiright",
	"BE":                  "Bundle-Ether",
	"tu":                  "Tunnel",
	"M-E":                 "M-Ethernet",
	"BAGG":                "Bridge-Aggregation",
	"Ten-GigabitEthernet": "TenGigabitEthernet",
	"Wl":                  "Wlan-GigabitEthernet",
	"Di":                  "Dialer",
	"Vi":                  "Virtual-Access",
	"Ce":                  "Cellular",
	"Vp":                  "Virtual-PPP",
	"pw":                  "pseudowire",
}

var (
	// reIntfType takes the letters before the first digit, the "Gi" of
	// "Gi0/0/1".
	reIntfType = re(`([-a-zA-Z]+)`)
	// reIntfPort takes everything from the first digit, the "0/0/1".
	reIntfPort = re(`(\d[\w./]*)`)
)

// convertIntfName is Common.convert_intf_name(intf) with genieparser's
// defaults (the generic table, case sensitive): an abbreviated interface
// name such as "Gi0/0/1" becomes "GigabitEthernet0/0/1". A type the table
// does not know keeps its own spelling, its first character capitalized,
// spaces removed and "ethernet" spelled "Ethernet".
func convertIntfName(intf string) string {
	t := reIntfType.search(intf)
	p := reIntfPort.search(intf)
	if t == nil || p == nil {
		return intf
	}
	if full, ok := intfAbbreviations[t.n(0)]; ok {
		return full + p.n(0)
	}
	n := firstRuneLen(intf)
	rest := strings.ReplaceAll(strings.ReplaceAll(intf[n:], " ", ""), "ethernet", "Ethernet")
	return capitalizeFirst(intf[:n]) + rest
}

// firstRuneLen is the byte length of s's first character.
func firstRuneLen(s string) int {
	for i := range s {
		if i > 0 {
			return i
		}
	}
	return len(s)
}
