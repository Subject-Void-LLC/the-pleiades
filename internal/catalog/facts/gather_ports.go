// Package facts: the ports subset's Linux probe (Phase 41c). One fact per
// listening socket, port.<protocol>:<address>:<port> = {protocol, address,
// port, uid, process}, read from the kernel's own tables in /proc/net rather
// than from ss or netstat: a minimal Debian or Rocky image has neither, and
// BusyBox's netstat names no process for a login that is not root. The
// process is found the way ss finds it, by matching each socket's inode to a
// file descriptor under /proc/<pid>/fd, so it is known only for the processes
// this login may look into (its own, or every one as root), and is left out,
// never guessed, for the rest. The socket's owner (uid) is always known.
package facts

import (
	"encoding/hex"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// portSectionMark separates the Linux ports probe's sections.
const portSectionMark = "--pleiades-ports--"

// linuxPortProbes reads, one section each: the machine's architecture (the
// kernel prints addresses in its own byte order), the four socket tables,
// every file descriptor this login can list, and each process's name. Every
// read is a file or a builtin but the one ls, so a host with thousands of
// processes costs one command, not thousands.
func linuxPortProbes() []gatherProbe {
	tables := ""
	for _, t := range []string{"tcp", "tcp6", "udp", "udp6"} {
		tables += `echo '` + portSectionMark + ` ` + t + `'; cat /proc/net/` + t + ` 2>/dev/null; `
	}
	return []gatherProbe{{
		subset: gatherSubsetPorts,
		family: gatherFamilyPort,
		command: `[ -r /proc/net/tcp ] && { uname -m; ` + tables +
			`echo '` + portSectionMark + ` fd'; ls -l /proc/[0-9]*/fd 2>/dev/null; ` +
			`echo '` + portSectionMark + ` comm'; for d in /proc/[0-9]*; do read -r c < "$d/comm" 2>/dev/null && printf '%s\t%s\n' "${d#/proc/}" "$c"; done; }`,
		parse: parseLinuxPorts,
	}}
}

// bigEndianMachines are the uname -m answers of Linux machines that store a
// word most significant byte first, whose /proc/net addresses read in order.
var bigEndianMachines = map[string]bool{"s390x": true, "s390": true, "ppc64": true, "ppc": true, "mips": true, "mips64": true, "sparc64": true, "armeb": true, "aarch64_be": true}

// listeningSocket is one socket a table lists as listening.
type listeningSocket struct {
	protocol, address, process string
	port                       int
	uid                        int
	inode                      string
}

// parseLinuxPorts reads linuxPortProbes' sections into one fact per
// listening socket: a TCP socket in state LISTEN (0A), and a UDP socket bound
// and unconnected (state 07, no remote port), which is UDP's listening.
func parseLinuxPorts(out string) []gatherFact {
	sections := strings.Split(out, portSectionMark)
	if len(sections) < 2 {
		return nil
	}
	bigEndian := bigEndianMachines[strings.TrimSpace(lastLine(sections[0]+"\n"))]
	var sockets []listeningSocket
	var fds, comms string
	for _, section := range sections[1:] {
		name, body, _ := strings.Cut(section, "\n")
		switch name = strings.TrimSpace(name); name {
		case "tcp", "tcp6", "udp", "udp6":
			sockets = append(sockets, parseProcNetTable(strings.TrimSuffix(name, "6"), body, bigEndian)...)
		case "fd":
			fds = body
		case "comm":
			comms = body
		}
	}
	owners := socketOwners(fds, comms)
	facts := make([]gatherFact, 0, len(sockets))
	for _, s := range sockets {
		value := map[string]any{"protocol": s.protocol, "address": s.address, "port": s.port, "uid": s.uid}
		if p := owners[s.inode]; p != "" {
			value["process"] = p
		}
		facts = append(facts, gatherFact{key: portKey(s.protocol, s.address, s.port), value: value})
	}
	return facts
}

// portKey is a listening socket's fact key: its protocol, address (an IPv6
// one in brackets) and port, as port.tcp:[::]:22.
func portKey(protocol, address string, port int) string {
	if strings.Contains(address, ":") {
		address = "[" + address + "]"
	}
	return itemKey(gatherFamilyPort, protocol+":"+address+":"+strconv.Itoa(port))
}

// parseProcNetTable reads one /proc/net table (sl, local, remote, state,
// queues, timer, retransmits, uid, timeout, inode, ...) for protocol.
func parseProcNetTable(protocol, body string, bigEndian bool) []listeningSocket {
	var out []listeningSocket
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || !strings.HasSuffix(f[0], ":") {
			continue
		}
		local, port, ok1 := procAddress(f[1], bigEndian)
		_, remotePort, ok2 := procAddress(f[2], bigEndian)
		uid, err := strconv.Atoi(f[7])
		if !ok1 || !ok2 || err != nil {
			continue
		}
		listening := (protocol == "tcp" && f[3] == "0A") || (protocol == "udp" && f[3] == "07" && remotePort == 0)
		if !listening {
			continue
		}
		out = append(out, listeningSocket{protocol: protocol, address: local, port: port, uid: uid, inode: f[9]})
	}
	return out
}

// procAddress reads a /proc/net address ("0100007F:0016"): the address as
// the kernel's words hold it (four bytes for IPv4, four words for IPv6, each
// word in the machine's byte order) and the port, a plain hex number.
func procAddress(field string, bigEndian bool) (string, int, bool) {
	hexAddr, hexPort, ok := strings.Cut(field, ":")
	raw, err := hex.DecodeString(hexAddr)
	port, err2 := strconv.ParseUint(hexPort, 16, 16)
	if !ok || err != nil || err2 != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, false
	}
	if !bigEndian {
		// Each 32-bit word was printed least significant byte first.
		for w := 0; w < len(raw); w += 4 {
			raw[w], raw[w+1], raw[w+2], raw[w+3] = raw[w+3], raw[w+2], raw[w+1], raw[w]
		}
	}
	addr, _ := netip.AddrFromSlice(raw)
	return addr.String(), int(port), true
}

// socketOwners maps each socket inode ls listed under a process's fd
// directory to that process's name: the lowest pid's when several hold it
// (a server's forked workers share its listener), so the answer does not
// move with which worker the kernel lists first.
func socketOwners(fds, comms string) map[string]string {
	names := map[int]string{}
	for _, line := range strings.Split(comms, "\n") {
		pid, name, ok := strings.Cut(line, "\t")
		if n, err := strconv.Atoi(pid); ok && err == nil && name != "" {
			names[n] = name
		}
	}
	holders := map[string][]int{}
	pid := -1
	for _, line := range strings.Split(fds, "\n") {
		if dir, ok := strings.CutSuffix(strings.TrimSpace(line), "/fd:"); ok {
			pid, _ = strconv.Atoi(strings.TrimPrefix(dir, "/proc/"))
			continue
		}
		_, target, ok := strings.Cut(line, "-> socket:[")
		inode, closed := strings.CutSuffix(strings.TrimSpace(target), "]")
		if ok && closed && pid > 0 {
			holders[inode] = append(holders[inode], pid)
		}
	}
	owners := map[string]string{}
	for inode, pids := range holders {
		sort.Ints(pids)
		for _, p := range pids {
			if names[p] != "" {
				owners[inode] = names[p]
				break
			}
		}
	}
	return owners
}
