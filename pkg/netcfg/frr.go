// Package netcfg: an FRRouting configuration laid out the way Batfish reads
// one.
package netcfg

import (
	"fmt"
	"regexp"
	"strings"
)

// hostnameLine is a configuration's hostname line.
var hostnameLine = regexp.MustCompile(`(?m)^hostname[ \t]+(\S+)[ \t]*$`)

// Hostname returns the hostname config names, "" when it names none.
func Hostname(config string) string {
	if m := hostnameLine.FindStringSubmatch(config); m != nil {
		return m[1]
	}
	return ""
}

// FRRForBatfish returns an FRRouting configuration as Batfish reads FRR:
// Cumulus's concatenated layout, the node's name, then an empty interfaces
// file and an empty ports.conf, then frr.conf. Batfish takes interface
// addresses from frr.conf alone (Phase 127b's spike 2), so the interfaces
// file stays empty.
//
// One translation is made, because Batfish models it only the other way
// round: each interface's "ip ospf passive" becomes "passive-interface
// <name>" under "router ospf" (spike 3). It reads blocks, so a passive line
// marks the interface whose block holds it and no other. A configuration
// with passive interfaces and no "router ospf" block is refused, since the
// translation would have nowhere to go and the model would then route
// through an interface the router keeps quiet.
func FRRForBatfish(name, config string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(config, "\r\n", "\n"), "\n")
	var out, passive []string
	iface := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		indented := line != "" && (line[0] == ' ' || line[0] == '\t')
		if !indented {
			iface = ""
			if f := strings.Fields(trimmed); len(f) == 2 && f[0] == "interface" {
				iface = f[1]
			}
		}
		if iface != "" && indented && trimmed == "ip ospf passive" {
			passive = append(passive, iface)
			continue
		}
		out = append(out, line)
	}
	if len(passive) > 0 {
		at := -1
		for i, line := range out {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "router" && f[1] == "ospf" && line[0] != ' ' {
				at = i
				break
			}
		}
		if at < 0 {
			return "", fmt.Errorf("%s marks %s passive for OSPF and has no router ospf block to say so in", name, strings.Join(passive, ", "))
		}
		add := make([]string, 0, len(passive))
		for _, p := range passive {
			add = append(add, " passive-interface "+p)
		}
		out = append(out[:at+1], append(add, out[at+1:]...)...)
	}
	return name + "\n# This file describes the network interfaces\n\n# ports.conf --\n\n" + strings.Join(out, "\n"), nil
}
