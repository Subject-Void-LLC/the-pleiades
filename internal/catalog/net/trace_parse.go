// Package net: reading iputils tracepath's answer. Beside each hop's address,
// tracepath prints "asymm N" on a hop whose reply came back over N hops
// rather than over as many as the probe went out, and ends a trace that
// reached its destination with "Resume: pmtu P hops H back B": the
// destination's reply came back over B hops. Both are read from the TTL a
// reply arrives with, assuming the sender started at 64, 128 or 255, so they
// count hops and never name the routers a reply passed: an asymmetric path
// of the same length reads as symmetric.
package net

import (
	"regexp"
	"strconv"
	"strings"
)

// traceAsymm is tracepath's "asymm N" on a hop line ("asymm %2d", so a
// one-digit count has two spaces before it).
var traceAsymm = regexp.MustCompile(`\basymm\s+(\d+)\b`)

// traceResume is tracepath's last line for a trace that reached its
// destination, "Resume: pmtu 1500 hops 3 back 2"; hops and back are each
// printed only when known.
var traceResume = regexp.MustCompile(`^\s*Resume:\s+pmtu\s+\d+(?:\s+hops\s+(\d+))?(?:\s+back\s+(\d+))?`)

// The path a trace found, as the path stat names it.
const (
	// pathReached is a trace whose destination answered.
	pathReached = "reached"
	// pathLoop is a trace that did not reach its destination, where one
	// address answered at two hops that are not next to each other: the
	// probe passed that router twice.
	pathLoop = "loop"
	// pathUnreached is a trace that did not reach its destination, where
	// some hop answered and none shows a loop: the path is known up to the
	// last hop that answered.
	pathUnreached = "unreached"
	// pathUnknown is a trace where no hop answered at all: a path filtered
	// at its first router, a loop whose routers send no ICMP, and a missing
	// route all read the same, so nothing is known.
	pathUnknown = "unknown"
)

// tracedHop is one hop of a trace.
type tracedHop struct {
	// n is the hop's number, the TTL its probe was sent with.
	n int
	// addr is the address that answered, or "*" when none did.
	addr string
	// back is the hop count tracepath printed after asymm, or 0 when it
	// printed none (the reply came back over n hops, or its TTL was not
	// read).
	back int
}

// tracedPath is a whole trace: its hops, whether the destination answered,
// and the destination's own hop counts from the Resume line, each -1 when
// tracepath did not print it.
type tracedPath struct {
	hops    []tracedHop
	reached bool
	// out is the Resume line's hops, back its back.
	out, back int
}

// parseTracepath reads iputils tracepath -n's answer: each hop's address
// once, in order ("no reply" kept as "*", so a hop's position holds), its
// asymm count, whether a line said reached, and the Resume line's counts.
func parseTracepath(text string) tracedPath {
	p := tracedPath{out: -1, back: -1}
	seen := map[int]int{}
	for _, line := range strings.Split(text, "\n") {
		if m := traceResume.FindStringSubmatch(line); m != nil {
			p.out, p.back = atoiOr(m[1]), atoiOr(m[2])
			continue
		}
		m := traceHop.FindStringSubmatch(line)
		if m == nil || m[2] == "[LOCALHOST]" {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			// A hop number past any int: not tracepath's.
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(line), "reached") {
			p.reached = true
		}
		addr := m[2]
		if addr == "no" {
			addr = "*"
		}
		back := 0
		if a := traceAsymm.FindStringSubmatch(line); a != nil && addr != "*" {
			// A count past any int is not tracepath's; it stays unread
			// (found by FuzzParseTracepath).
			back = max(atoiOr(a[1]), 0)
		}
		// tracepath prints a hop again for a second probe; the first line
		// names its address, and an asymm count on either is kept.
		if at, ok := seen[n]; ok {
			if p.hops[at].back <= 0 && back > 0 && p.hops[at].addr == addr {
				p.hops[at].back = back
			}
			continue
		}
		seen[n] = len(p.hops)
		p.hops = append(p.hops, tracedHop{n: n, addr: addr, back: back})
	}
	return p
}

// atoiOr is s as a number, or -1 when s is empty or not one.
func atoiOr(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

// addresses is each hop's address, in order.
func (p tracedPath) addresses() []string {
	out := make([]string, len(p.hops))
	for i, h := range p.hops {
		out[i] = h.addr
	}
	return out
}

// state names the path: reached, loop, unreached or unknown. A loop is
// read only from addresses that answered, an address at two hops with
// another hop between them, never from hops that did not answer: a run of
// "*" is a filter, a rate limit or a loop alike. The same address at two
// hops next to each other is not one either, since a router that does not
// count down the TTL answers twice in a row.
func (p tracedPath) state() string {
	if p.reached {
		return pathReached
	}
	at := map[string]int{}
	answered := false
	for i, h := range p.hops {
		if h.addr == "*" {
			continue
		}
		answered = true
		if first, ok := at[h.addr]; ok && i > first+1 {
			return pathLoop
		}
		at[h.addr] = i
	}
	if !answered {
		return pathUnknown
	}
	return pathUnreached
}

// returnCounts are the destination's hop counts out and back, and whether
// tracepath printed both.
func (p tracedPath) returnCounts() (out, back int, ok bool) {
	if !p.reached || p.back < 0 {
		return 0, 0, false
	}
	out = p.out
	if out < 0 && len(p.hops) > 0 {
		// No hops on the Resume line: the reached hop's own number.
		out = p.hops[len(p.hops)-1].n
	}
	return out, p.back, out >= 0
}

// hopDetails is the hop_details stat: each hop's number and address, and
// return_hops on a hop whose reply came back over a different number of
// hops. tracepath prints no asymm on the line that reached the
// destination (measured on the CML lab's Ubuntu hosts and on the FRR test
// routers' Alpine), only
// back on the Resume line, so the destination's hop takes its count from
// there.
func (p tracedPath) hopDetails() []any {
	out := make([]any, len(p.hops))
	for i, h := range p.hops {
		d := map[string]any{"hop": h.n, "address": h.addr}
		back := h.back
		if i == len(p.hops)-1 && back <= 0 {
			if o, b, ok := p.returnCounts(); ok && o == h.n && b != o {
				back = b
			}
		}
		if back > 0 {
			d["return_hops"] = back
		}
		out[i] = d
	}
	return out
}
