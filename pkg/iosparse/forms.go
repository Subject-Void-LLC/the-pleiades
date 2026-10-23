// Ported from genieparser's src/genie/libs/parser/iosxe/show_platform.py,
// src/genie/libs/parser/iosxe/show_interface.py,
// src/genie/libs/parser/iosxe/show_routing.py,
// src/genie/libs/parser/iosxe/show_ospf.py,
// src/genie/libs/parser/iosxe/show_bgp.py,
// src/genie/libs/parser/iosxe/show_ip_bgp.py,
// src/genie/libs/parser/iosxe/show_vlan.py,
// src/genie/libs/parser/iosxe/show_cdp.py,
// src/genie/libs/parser/iosxe/show_lldp.py,
// src/genie/libs/parser/iosxe/show_archive.py (commit e62f9467), Copyright
// (c) 2021 Cisco Systems, Inc. and/or its affiliates, Apache License 2.0;
// changed: translated from Python to Go. See NOTICE.genieparser.

// The command forms each ported parser covers.
package iosparse

import "strings"

// Argument expressions for the command forms.
const (
	// argIntf is one interface name, abbreviated or not, with or without a
	// space before its number: "Gi1/0/1", "GigabitEthernet 0/0/0". It needs
	// a digit, so "show interfaces status" and "show cdp neighbors detail",
	// which other genieparser classes parse, are never taken for it.
	argIntf = `[A-Za-z][A-Za-z-]*\s?\d[\w./:]*`
	// argWord is one word, such as a VRF or file name.
	argWord = `\S+`
	// argFamily is a BGP address family of one or two words, "vpnv4" or
	// "ipv4 unicast".
	argFamily = `[A-Za-z0-9-]+(?:\s+[A-Za-z0-9-]+)?`
	// argProtocol is a routing protocol ShowIpRoute takes, genieparser's
	// ShowIpRouteDistributor.protocol_set. Any other word after
	// "show ip route" is a prefix, which a different parser reads.
	argProtocol = `ospf|odr|isis|eigrp|static|mobile|rip|lisp|nhrp|local|connected|bgp|multicast`
)

// The ported parsers.
var (
	defShowVersion          = &parserDef{name: "ShowVersion", parse: parseShowVersion, schema: schemaShowVersion}
	defShowInventory        = &parserDef{name: "ShowInventory", parse: parseShowInventory, schema: schemaShowInventory}
	defShowIpInterfaceBrief = &parserDef{name: "ShowIpInterfaceBrief", parse: parseShowIpInterfaceBrief, schema: schemaShowIpInterfaceBrief}
	defShowInterfaces       = &parserDef{name: "ShowInterfaces", parse: parseShowInterfaces, schema: schemaShowInterfaces}
	defShowIpRoute          = &parserDef{name: "ShowIpRoute", parse: parseShowIpRoute, schema: schemaShowIpRoute}
	defShowIpOspfNeighbor   = &parserDef{name: "ShowIpOspfNeighbor", parse: parseShowIpOspfNeighbor, schema: schemaShowIpOspfNeighbor}
	defShowIpBgpSummary     = &parserDef{name: "ShowIpBgpSummary", parse: parseBgpSummary, schema: schemaShowBgpSummary}
	defShowBgpSummary       = &parserDef{name: "ShowBgpSummary", parse: parseBgpSummary, schema: schemaShowBgpSummary}
	defShowVlan             = &parserDef{name: "ShowVlan", parse: parseShowVlan, schema: schemaShowVlan}
	defShowCdpNeighbors     = &parserDef{name: "ShowCdpNeighbors", parse: parseShowCdpNeighbors, schema: schemaShowCdpNeighbors}
	defShowLldpNeighbors    = &parserDef{name: "ShowLldpNeighbors", parse: parseShowLldpNeighbors, schema: schemaShowLldpNeighbors}
	defShowArchiveDiffs     = &parserDef{name: "ShowArchiveConfigDifferences", parse: parseShowArchiveConfigDifferences, schema: schemaShowArchiveConfigDifferences}
)

// bgpRefusal refuses a BGP summary command that makes genieparser run a
// second command on the device: any whose text holds "rd" (genieparser
// then runs "show vrf") or "all summary" outside IPv4 and IPv6 unicast
// (it then runs "show running-config"). Its output alone cannot be parsed
// as genieparser parses it. prefix is "show ip bgp" or "show bgp".
func bgpRefusal(prefix string) func(map[string]string) string {
	return func(args map[string]string) string {
		cmd := prefix + " summary"
		switch {
		case args["vrf"] != "":
			cmd = prefix + " " + args["address_family"] + " vrf " + args["vrf"] + " summary"
		case args["address_family"] != "":
			cmd = prefix + " " + args["address_family"] + " summary"
		}
		family := strings.ToLower(args["address_family"])
		if strings.Contains(cmd, "rd") || (strings.Contains(cmd, "all summary") && family != "ipv4 unicast" && family != "ipv6 unicast") {
			return "genieparser parses this form with the output of a second command (show vrf or show running-config) as well, which net.ios.parse does not run"
		}
		for _, word := range strings.Fields(family) {
			if word == "vrf" || word == "rd" || word == "all" || word == "summary" {
				return "the address family cannot be " + word
			}
		}
		return ""
	}
}

func init() {
	register(defShowVersion, nil, [2]string{"show version", `show\s+version`})
	register(defShowInventory, nil, [2]string{"show inventory", `show\s+inventory`})
	register(defShowIpInterfaceBrief, nil,
		[2]string{"show ip interface brief", `show\s+ip\s+interface\s+brief`},
		[2]string{"show ip interface brief {interface}", `show\s+ip\s+interface\s+brief\s+(?P<interface>` + argIntf + `)`})
	register(defShowInterfaces, nil,
		[2]string{"show interfaces", `show\s+interfaces`},
		[2]string{"show interfaces {interface}", `show\s+interfaces\s+(?P<interface>` + argIntf + `)`},
		[2]string{"show interfaces | include {include}", `show\s+interfaces\s*\|\s*include\s+(?P<include>.+)`})
	register(defShowIpRoute, nil,
		[2]string{"show ip route", `show\s+ip\s+route`},
		[2]string{"show ip route {protocol}", `show\s+ip\s+route\s+(?P<protocol>` + argProtocol + `)`},
		[2]string{"show ip route vrf {vrf}", `show\s+ip\s+route\s+vrf\s+(?P<vrf>` + argWord + `)`},
		[2]string{"show ip route vrf {vrf} {protocol}", `show\s+ip\s+route\s+vrf\s+(?P<vrf>` + argWord + `)\s+(?P<protocol>` + argProtocol + `)`})
	register(defShowIpOspfNeighbor, nil,
		[2]string{"show ip ospf neighbor", `show\s+ip\s+ospf\s+neighbor`},
		[2]string{"show ip ospf neighbor {interface}", `show\s+ip\s+ospf\s+neighbor\s+(?P<interface>` + argIntf + `)`},
		[2]string{"show ip ospf {process_id} neighbor", `show\s+ip\s+ospf\s+(?P<process_id>\d+)\s+neighbor`},
		[2]string{"show ip ospf {process_id} neighbor {interface}", `show\s+ip\s+ospf\s+(?P<process_id>\d+)\s+neighbor\s+(?P<interface>` + argIntf + `)`})
	register(defShowIpBgpSummary, bgpRefusal("show ip bgp"),
		[2]string{"show ip bgp summary", `show\s+ip\s+bgp\s+summary`},
		[2]string{"show ip bgp {address_family} vrf {vrf} summary", `show\s+ip\s+bgp\s+(?P<address_family>` + argFamily + `)\s+vrf\s+(?P<vrf>` + argWord + `)\s+summary`},
		[2]string{"show ip bgp {address_family} summary", `show\s+ip\s+bgp\s+(?P<address_family>` + argFamily + `)\s+summary`})
	register(defShowBgpSummary, bgpRefusal("show bgp"),
		[2]string{"show bgp summary", `show\s+bgp\s+summary`},
		[2]string{"show bgp {address_family} vrf {vrf} summary", `show\s+bgp\s+(?P<address_family>` + argFamily + `)\s+vrf\s+(?P<vrf>` + argWord + `)\s+summary`},
		[2]string{"show bgp {address_family} summary", `show\s+bgp\s+(?P<address_family>` + argFamily + `)\s+summary`})
	register(defShowVlan, nil, [2]string{"show vlan", `show\s+vlan`})
	register(defShowCdpNeighbors, nil,
		[2]string{"show cdp neighbors", `show\s+cdp\s+neighbors`},
		[2]string{"show cdp neighbors {interface}", `show\s+cdp\s+neighbors\s+(?P<interface>` + argIntf + `)`})
	register(defShowLldpNeighbors, nil, [2]string{"show lldp neighbors", `show\s+lldp\s+neighbors`})
	register(defShowArchiveDiffs, nil,
		[2]string{"show archive config differences", `show\s+archive\s+config\s+differences`},
		[2]string{"show archive config differences {fileA}", `show\s+archive\s+config\s+differences\s+(?P<fileA>` + argWord + `)`},
		[2]string{"show archive config differences {fileA} {fileB}", `show\s+archive\s+config\s+differences\s+(?P<fileA>` + argWord + `)\s+(?P<fileB>` + argWord + `)`})
}
