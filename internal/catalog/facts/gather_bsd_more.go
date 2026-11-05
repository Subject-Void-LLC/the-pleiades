// Package facts: the ports and users probes of FreeBSD and macOS (Phase
// 41c). FreeBSD's listening sockets come from sockstat, which names every
// socket's process unless security.bsd.see_other_uids hides other accounts'
// (then it prints ?, read as unknown). A Mac's come from netstat -anv, which
// on macOS 27 names every socket's process to any login; lsof was not used,
// since without root it sees only the login's own processes. A Mac's accounts
// come from its local directory through dscl; dscacheutil lists each twice,
// and /etc/passwd holds only a few of them.
package facts

import (
	"net/netip"
	"strconv"
	"strings"
)

// freebsdPortProbe lists every listening TCP socket and bound UDP socket,
// IPv4 and IPv6, without the header line.
func freebsdPortProbe() gatherProbe {
	return gatherProbe{
		subset:  gatherSubsetPorts,
		family:  gatherFamilyPort,
		present: `command -v sockstat >/dev/null 2>&1`,
		command: orFailed(`LC_ALL=C sockstat -46lq`),
		parse:   parseSockstat,
	}
}

// parseSockstat reads sockstat's lines (user, command, pid, fd, protocol,
// local and foreign address). The command may hold spaces, so a line is read
// from its protocol column, the one field naming tcp or udp and a family. A
// UDP socket listens when it has no foreign address (*:*). sockstat prints a
// socket once for each process holding it (a server's forked workers share
// its listener), so lines naming one socket are one fact, named by the
// lowest pid's command as Linux names it, and their number, which moves as
// workers come and go, never reaches a key.
func parseSockstat(out string) []gatherFact {
	type owner struct {
		pid     int
		command string
	}
	var order []string
	values := map[string]map[string]any{}
	owners := map[string]owner{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		i := 0
		for i < len(f) && socketProtocol(f[i]) == "" {
			i++
		}
		// user, at least one word of command, pid and fd come first, and the
		// local and foreign addresses after.
		if i < 4 || i+2 >= len(f) {
			continue
		}
		protocol := socketProtocol(f[i])
		if protocol == "udp" && f[i+2] != "*:*" {
			continue
		}
		address, port, ok := splitHostPort(f[i+1], ":", f[i])
		if !ok {
			continue
		}
		key := portKey(protocol, address, port)
		if _, seen := values[key]; !seen {
			order = append(order, key)
			values[key] = map[string]any{"protocol": protocol, "address": address, "port": port}
			owners[key] = owner{pid: -1}
		}
		// A hidden owner (?) names nothing; a known one is kept when its pid
		// is the lowest seen.
		command := strings.Join(f[1:i-2], " ")
		pid, err := strconv.Atoi(f[i-2])
		if command == "?" || err != nil {
			continue
		}
		if o := owners[key]; o.pid < 0 || pid < o.pid {
			owners[key] = owner{pid: pid, command: command}
		}
	}
	facts := make([]gatherFact, 0, len(order))
	for _, key := range order {
		if o := owners[key]; o.pid >= 0 {
			values[key]["process"] = o.command
		}
		facts = append(facts, gatherFact{key: key, value: values[key]})
	}
	return facts
}

// socketProtocol is tcp or udp for a BSD protocol column (tcp4, tcp6,
// tcp46, udp4, udp6, udp46), or "" for anything else.
func socketProtocol(field string) string {
	for _, p := range []string{"tcp", "udp"} {
		if family, ok := strings.CutPrefix(field, p); ok && (family == "4" || family == "6" || family == "46") {
			return p
		}
	}
	return ""
}

// splitHostPort splits a BSD socket address at its last sep (":" from
// sockstat, "." from macOS netstat) into its address and port. A wildcard (*)
// is the protocol column's family's any address, :: for a socket taking both
// families; brackets and a scope (%lo0) are kept as the tool wrote them,
// brackets removed. A port that is not a number (*, unbound) is not one.
func splitHostPort(field, sep, protocolColumn string) (string, int, bool) {
	i := strings.LastIndex(field, sep)
	if i <= 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(field[i+1:])
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, false
	}
	address := strings.TrimSuffix(strings.TrimPrefix(field[:i], "["), "]")
	if address == "*" {
		address = "::"
		if strings.HasSuffix(protocolColumn, "4") && !strings.HasSuffix(protocolColumn, "46") {
			address = "0.0.0.0"
		}
	}
	if a, err := netip.ParseAddr(address); err == nil {
		address = a.String()
	}
	return address, port, true
}

// darwinPortProbe lists every TCP and UDP socket with its process, the TCP
// ones first, a mark between.
func darwinPortProbe() gatherProbe {
	return gatherProbe{
		subset: gatherSubsetPorts,
		family: gatherFamilyPort,
		// -l prints an IPv6 address whole; without it netstat cuts one to
		// its column's width.
		present: `command -v netstat >/dev/null 2>&1`,
		command: orFailed(`LC_ALL=C netstat -anvl -p tcp`) + `; echo '` + portSectionMark + `'; ` + orFailed(`LC_ALL=C netstat -anvl -p udp`),
		parse:   parseDarwinNetstat,
	}
}

// darwinTrailingColumns is how many columns macOS netstat -anv prints after
// process:pid (state, options, gencnt, flags, flags1, usecnt, rtncnt,
// fltrs). The process name may hold spaces, so it is read between the fixed
// columns before it and these after it.
const darwinTrailingColumns = 8

// parseDarwinNetstat reads netstat -anv's rows: protocol, queues, local and
// foreign address, a TCP state (UDP has none), four byte and buffer counts,
// process:pid, then darwinTrailingColumns more. A TCP socket listens in state
// LISTEN; a UDP socket when its foreign address is *.*.
func parseDarwinNetstat(out string) []gatherFact {
	var facts []gatherFact
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		protocol := socketProtocol(f[0])
		// The columns before process:pid: six for TCP (with its state), five
		// for UDP, then the four counts.
		lead := 10
		if protocol == "udp" {
			lead = 9
		}
		if protocol == "" || len(f) < lead+1+darwinTrailingColumns {
			continue
		}
		if (protocol == "tcp" && f[5] != "LISTEN") || (protocol == "udp" && f[4] != "*.*") {
			continue
		}
		address, port, ok := splitHostPort(f[3], ".", f[0])
		if !ok {
			continue
		}
		value := map[string]any{"protocol": protocol, "address": address, "port": port}
		// The process is named only when the column reads as name:pid, so
		// a row of another layout names nothing rather than a guess.
		owner := strings.Join(f[lead:len(f)-darwinTrailingColumns], " ")
		if i := strings.LastIndex(owner, ":"); i > 0 {
			if _, err := strconv.ParseUint(owner[i+1:], 10, 32); err == nil {
				value["process"] = owner[:i]
			}
		}
		facts = append(facts, gatherFact{key: portKey(protocol, address, port), value: value})
	}
	return facts
}

// sourceDirectory is where a Mac's account facts were read: its local
// directory node.
const sourceDirectory = "dslocal"

// darwinUserProbes reads the local directory's users and groups through
// dscl, naming only the attributes wanted, so no password or its hash is
// asked for.
func darwinUserProbes() []gatherProbe {
	return []gatherProbe{
		{
			subset:  gatherSubsetUsers,
			family:  gatherFamilyUser,
			present: `command -v dscl >/dev/null 2>&1`,
			command: orFailed(`dscl . -readall /Users RecordName UniqueID PrimaryGroupID NFSHomeDirectory UserShell`),
			parse:   parseDsclUsers,
			check:   listsSomething("dscl /Users"),
		},
		{
			subset:  gatherSubsetUsers,
			family:  gatherFamilyGroup,
			present: `command -v dscl >/dev/null 2>&1`,
			command: orFailed(`dscl . -readall /Groups RecordName PrimaryGroupID GroupMembership`),
			check:   listsSomething("dscl /Groups"),
			parse:   parseDsclGroups,
		},
	}
}

// dsclRecords reads dscl -readall's records, separated by a line holding
// only -, into each attribute's values. An attribute printed on one line
// ("RecordName: _amavisd amavisd") has its values separated by spaces; one
// whose value holds a space is printed as the name alone and then one value
// per line, each indented by a space.
func dsclRecords(out string) []map[string][]string {
	var records []map[string][]string
	current := map[string][]string{}
	last := ""
	flush := func() {
		if len(current) > 0 {
			records = append(records, current)
		}
		current, last = map[string][]string{}, ""
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "-":
			flush()
		case strings.HasPrefix(line, " ") && last != "":
			current[last] = append(current[last], strings.TrimSpace(line))
		default:
			name, values, ok := strings.Cut(line, ":")
			if !ok || name == "" || strings.Contains(name, " ") {
				last = ""
				continue
			}
			last = name
			current[name] = append(current[name], strings.Fields(values)...)
		}
	}
	flush()
	return records
}

// dsclFirst is a record's attribute's first value, or "".
func dsclFirst(record map[string][]string, name string) string {
	if v := record[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// parseDsclUsers reads the users' records: name (the first RecordName),
// uid, gid, home and shell. A record whose uid or gid is not a number is not
// an account this can describe.
func parseDsclUsers(out string) []gatherFact {
	var facts []gatherFact
	for _, r := range dsclRecords(out) {
		name := dsclFirst(r, "RecordName")
		uid, err1 := strconv.Atoi(dsclFirst(r, "UniqueID"))
		gid, err2 := strconv.Atoi(dsclFirst(r, "PrimaryGroupID"))
		if name == "" || err1 != nil || err2 != nil {
			continue
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyUser, name), value: map[string]any{
			"uid": uid, "gid": gid, "home": dsclFirst(r, "NFSHomeDirectory"), "shell": dsclFirst(r, "UserShell"), "source": sourceDirectory,
		}})
	}
	return facts
}

// parseDsclGroups reads the groups' records: name, gid and members.
func parseDsclGroups(out string) []gatherFact {
	var facts []gatherFact
	for _, r := range dsclRecords(out) {
		name := dsclFirst(r, "RecordName")
		gid, err := strconv.Atoi(dsclFirst(r, "PrimaryGroupID"))
		if name == "" || err != nil {
			continue
		}
		members := append([]string{}, r["GroupMembership"]...)
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyGroup, name), value: map[string]any{
			"gid": gid, "members": members, "source": sourceDirectory,
		}})
	}
	return facts
}
