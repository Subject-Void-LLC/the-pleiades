// Package pcapsum: each IP packet's DSCP, counted by value with the name of
// its codepoint and the RFC 4594 service class that codepoint is
// recommended for, so a capture shows the classes its traffic was marked
// with. The marks are read, never judged: a value is what the sender (or a
// router on the way) wrote, which is not proof of how any queue treated it.
package pcapsum

import "sort"

// DSCPCount is the packets and bytes marked with one DSCP value.
type DSCPCount struct {
	// DSCP is the value, 0 to 63.
	DSCP int `json:"dscp"`
	// Name is the codepoint's name (EF, AF41, CS6, DF, LE, VOICE-ADMIT),
	// or "" for a value no standard names.
	Name string `json:"name"`
	// Class is the RFC 4594 service class the codepoint is recommended
	// for (Figure 3 of RFC 4594), or "" when RFC 4594 gives it none.
	Class string `json:"class"`
	// Packets and Bytes count what was marked with it.
	Packets int   `json:"packets"`
	Bytes   int64 `json:"bytes"`
}

// codepoints names the standard codepoints: CS0 to CS7 (RFC 2474), AF11 to
// AF43 (RFC 2597), EF (RFC 3246), VOICE-ADMIT (RFC 5865) and LE (RFC 8622).
// CS0 is named DF, the default forwarding it means.
var codepoints = map[int]string{
	0: "DF", 8: "CS1", 16: "CS2", 24: "CS3", 32: "CS4", 40: "CS5", 48: "CS6", 56: "CS7",
	10: "AF11", 12: "AF12", 14: "AF13", 18: "AF21", 20: "AF22", 22: "AF23",
	26: "AF31", 28: "AF32", 30: "AF33", 34: "AF41", 36: "AF42", 38: "AF43",
	46: "EF", 44: "VOICE-ADMIT", 1: "LE",
}

// serviceClasses is RFC 4594's Figure 3: the service class each codepoint
// is recommended for. CS7 is reserved there, and VOICE-ADMIT and LE came
// after it, so none of the three has a class here.
var serviceClasses = map[int]string{
	48: "Network Control",
	46: "Telephony",
	40: "Signaling",
	34: "Multimedia Conferencing", 36: "Multimedia Conferencing", 38: "Multimedia Conferencing",
	32: "Real-Time Interactive",
	26: "Multimedia Streaming", 28: "Multimedia Streaming", 30: "Multimedia Streaming",
	24: "Broadcast Video",
	18: "Low-Latency Data", 20: "Low-Latency Data", 22: "Low-Latency Data",
	16: "OAM",
	10: "High-Throughput Data", 12: "High-Throughput Data", 14: "High-Throughput Data",
	0: "Standard",
	8: "Low-Priority Data",
}

// DSCPName is the codepoint name and RFC 4594 service class of value,
// each "" when none is given.
func DSCPName(value int) (name, class string) {
	return codepoints[value], serviceClasses[value]
}

// dscpTally counts packets and bytes by DSCP value.
type dscpTally map[int]Counter

// add counts one packet of n bytes marked value.
func (t dscpTally) add(value, n int) {
	c := t[value]
	c.add(n)
	t[value] = c
}

// list is t as DSCPCounts, most bytes first, then by value.
func (t dscpTally) list() []DSCPCount {
	if len(t) == 0 {
		return nil
	}
	out := make([]DSCPCount, 0, len(t))
	for v, c := range t {
		name, class := DSCPName(v)
		out = append(out, DSCPCount{DSCP: v, Name: name, Class: class, Packets: c.Packets, Bytes: c.Bytes})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].DSCP < out[j].DSCP
	})
	return out
}
