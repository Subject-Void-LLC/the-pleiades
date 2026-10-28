// net.ios.facts: the interfaces subset, each interface's address and state
// from "show ip interface brief", its description, MTU and states from
// "show interfaces", and its VRF from "show vrf".
package ios

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// The interface map's keys.
const (
	ifName         = "name"
	ifIPAddress    = "ip_address"
	ifStatus       = "status"
	ifProtocol     = "protocol"
	ifDescription  = "description"
	ifMTU          = "mtu"
	ifAdminState   = "admin_state"
	ifLineProtocol = "line_protocol"
	ifVRF          = "vrf"
)

// The VRF commands, the first answering one wins: "show vrf" lists IPv4 and
// IPv6 VRFs alike, and an older image that refuses it answers "show ip vrf
// brief", whose columns are the same less Protocols. Both answer in user
// mode on IOL XE 17.15.1.
var vrfCommands = []string{"show vrf", "show ip vrf brief"}

var (
	// "Ethernet0/3 is administratively down, line protocol is down ", the
	// first line of each interface in show interfaces, at the left margin.
	// The line protocol is one word and an optional parenthesis ("down
	// (notconnect)"), so a switch's ", Autostate Enabled" after it is not
	// read as part of it.
	reIfaceHeader = regexp.MustCompile(`^(\S+) is (.+?), line protocol is (\S+(?: \([^)]*\))?)`)
	// "  Description: to port1.sandbox-backend"
	reIfaceDescription = regexp.MustCompile(`^\s+Description: (.*?)\s*$`)
	// "  MTU 1500 bytes, BW 10000 Kbit/sec, DLY 1000 usec, "
	reIfaceMTU = regexp.MustCompile(`^\s+MTU (\d+) bytes`)
)

// gatherInterfaces reads the three commands and joins them by name into
// g's ansible_net_interfaces.
func gatherInterfaces(ctx context.Context, session iosSession, g *gathered) error {
	brief, err := show(ctx, session, "show ip interface brief")
	if err != nil {
		return err
	}
	detail, err := show(ctx, session, "show interfaces")
	if err != nil {
		return err
	}
	interfaces := mergeInterfaces(parseInterfaces(brief), parseInterfaceDetails(detail))

	vrfOut, answered, err := showVRFs(ctx, session)
	if err != nil {
		return err
	}
	if !answered {
		g.warnings = append(g.warnings, "the device answered neither show vrf nor show ip vrf brief, so no interface has a vrf")
	}
	members, err := parseVRFs(vrfOut)
	if err != nil {
		return err
	}
	g.warnings = append(g.warnings, assignVRFs(interfaces, members)...)
	g.values[factInterfaces] = interfaces
	return nil
}

// showVRFs returns the first VRF command's output the device answers, and
// false when it refuses both. A transport failure is returned as it came.
func showVRFs(ctx context.Context, session iosSession) (string, bool, error) {
	for _, command := range vrfCommands {
		out, err := show(ctx, session, command)
		var refused *errRefused
		if errors.As(err, &refused) {
			continue
		}
		return out, err == nil, err
	}
	return "", false, nil
}

// parseInterfaces reads "show ip interface brief". It splits on fields
// rather than fixed columns, and takes the LAST field as the protocol
// with everything between the method and it as the status, because a real
// status can be two words ("administratively down") while a protocol is
// always one. A fixed-width or first-token parse silently truncates that
// case, which is exactly the sort of thing only real output reveals.
func parseInterfaces(out string) []map[string]any {
	interfaces := []map[string]any{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if fields[0] == "Interface" {
			continue
		}
		if fields[2] != "YES" && fields[2] != "NO" {
			continue
		}
		entry := map[string]any{
			ifName:     fields[0],
			ifStatus:   strings.Join(fields[4:len(fields)-1], " "),
			ifProtocol: fields[len(fields)-1],
		}
		// IOS writes "unassigned" where a real device has no address.
		// Reporting that string as an ip_address would make every
		// unaddressed interface look addressed to a CEL condition.
		if fields[1] != "unassigned" {
			entry[ifIPAddress] = fields[1]
		}
		interfaces = append(interfaces, entry)
	}
	return interfaces
}

// interfaceDetail is what "show interfaces" says of one interface.
type interfaceDetail struct {
	name         string
	status       string
	lineProtocol string
	description  string
	mtu          int
}

// parseInterfaceDetails reads "show interfaces": each interface opens at
// the left margin with its states, and its description and MTU follow on
// indented lines. The result keeps the device's order.
func parseInterfaceDetails(out string) []interfaceDetail {
	var details []interfaceDetail
	for _, line := range strings.Split(out, "\n") {
		if m := reIfaceHeader.FindStringSubmatch(line); m != nil {
			details = append(details, interfaceDetail{name: m[1], status: m[2], lineProtocol: m[3]})
			continue
		}
		if len(details) == 0 {
			continue
		}
		current := &details[len(details)-1]
		if m := reIfaceDescription.FindStringSubmatch(line); m != nil {
			current.description = m[1]
		} else if m := reIfaceMTU.FindStringSubmatch(line); m != nil {
			// The expression admits digits alone, so this cannot fail
			// short of a number past int's range, which no MTU is.
			current.mtu, _ = strconv.Atoi(m[1])
		}
	}
	return details
}

// adminState is the administrative state a show interfaces status implies:
// "administratively down" is an interface shut down, "up" and "down" one
// that is not. Any other status ("deleted") implies none.
func adminState(status string) (string, bool) {
	switch status {
	case "administratively down":
		return "down", true
	case "up", "down":
		return "up", true
	}
	return "", false
}

// mergeInterfaces adds each interface's details to its brief entry, by
// name, and appends one for an interface only show interfaces listed.
func mergeInterfaces(brief []map[string]any, details []interfaceDetail) []map[string]any {
	byName := make(map[string]map[string]any, len(brief))
	for _, entry := range brief {
		byName[entry[ifName].(string)] = entry
	}
	for _, d := range details {
		entry, ok := byName[d.name]
		if !ok {
			entry = map[string]any{ifName: d.name}
			byName[d.name] = entry
			brief = append(brief, entry)
		}
		if d.description != "" {
			entry[ifDescription] = d.description
		}
		if d.mtu > 0 {
			entry[ifMTU] = d.mtu
		}
		if state, ok := adminState(d.status); ok {
			entry[ifAdminState] = state
		}
		entry[ifLineProtocol] = d.lineProtocol
	}
	return brief
}

// vrfMember is one interface a VRF table names, as the device abbreviates
// it ("Et0/2").
type vrfMember struct {
	vrf   string
	iface string
}

// parseVRFs reads "show vrf" or "show ip vrf brief": a header naming its
// columns, then one row per VRF whose interfaces start under the header's
// "Interfaces", and a continuation row, blank up to that column, for each
// further interface of the same VRF. Columns are found from the header
// rather than by splitting on spaces, because the Default RD column can
// read "<not set>", two words. Output with no header is refused, since
// without one nothing says which interfaces are in which VRF; output with
// nothing at all is a device with no VRFs.
func parseVRFs(out string) ([]vrfMember, error) {
	lines := strings.Split(out, "\n")
	header := -1
	col := -1
	for i, line := range lines {
		if strings.Contains(line, "Name") && strings.Contains(line, "Default RD") {
			header, col = i, strings.Index(line, "Interfaces")
			break
		}
	}
	if header < 0 || col < 0 {
		if strings.TrimSpace(out) == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("the VRF table has no header naming its Interfaces column, so which interface is in which VRF cannot be read")
	}
	var members []vrfMember
	vrf := ""
	for _, line := range lines[header+1:] {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line == "" {
			continue
		}
		before, after := line, ""
		if len(line) > col {
			before, after = line[:col], line[col:]
		}
		if name := strings.Fields(before); len(name) > 0 {
			vrf = name[0]
		}
		if vrf == "" {
			continue
		}
		for _, iface := range strings.Fields(after) {
			members = append(members, vrfMember{vrf: vrf, iface: iface})
		}
	}
	return members, nil
}

// assignVRFs sets each member's vrf on the interface it names, matching the
// device's abbreviation to the one interface it can stand for, and returns
// a warning for each member that matches none or several.
func assignVRFs(interfaces []map[string]any, members []vrfMember) []string {
	var warnings []string
	for _, m := range members {
		entry := matchInterface(interfaces, m.iface)
		if entry == nil {
			warnings = append(warnings, fmt.Sprintf("VRF %s names interface %s, which matches no one interface this device listed, so no interface has that vrf from it", m.vrf, m.iface))
			continue
		}
		entry[ifVRF] = m.vrf
	}
	return warnings
}

// matchInterface is the one interface abbrev names: the interface of that
// exact name, or else the one whose number is abbrev's and whose type
// starts with abbrev's letters ("Et0/2" for "Ethernet0/2", "Gi1" for
// "GigabitEthernet1"), compared without case. Nil when none or several do.
func matchInterface(interfaces []map[string]any, abbrev string) map[string]any {
	for _, entry := range interfaces {
		if entry[ifName] == abbrev {
			return entry
		}
	}
	var found map[string]any
	letters, number := splitInterfaceName(abbrev)
	for _, entry := range interfaces {
		name, _ := entry[ifName].(string)
		l, n := splitInterfaceName(name)
		if n != number || letters == "" || !strings.HasPrefix(strings.ToLower(l), strings.ToLower(letters)) {
			continue
		}
		if found != nil {
			return nil
		}
		found = entry
	}
	return found
}

// splitInterfaceName splits an interface name at its first digit into its
// type ("Ethernet", "Port-channel") and its number ("0/2", "1.100").
func splitInterfaceName(name string) (string, string) {
	i := strings.IndexFunc(name, unicode.IsDigit)
	if i < 0 {
		return name, ""
	}
	return name[:i], name[i:]
}
