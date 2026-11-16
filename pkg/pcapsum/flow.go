// Package pcapsum: TCP flows. Each whole TCP segment is kept, as a slice of
// the capture and never a copy, under its flow and direction, and counted
// only once the capture has been read: each direction is then put in
// sequence order and read by the parser its first bytes call for, so a
// retransmission, a segment out of order and a segment the capture lost are
// all read where they belong rather than where they arrived.
package pcapsum

import (
	"encoding/binary"
	"net/netip"
	"sort"
)

// The TCP flags read.
const (
	tcpSYN = 0x02
	tcpACK = 0x10
)

// endpoint is one side of a flow.
type endpoint struct {
	addr netip.Addr
	port uint16
}

// less orders endpoints by address, then port.
func (e endpoint) less(o endpoint) bool {
	if e.addr != o.addr {
		return e.addr.Less(o.addr)
	}
	return e.port < o.port
}

// flowKey is a flow's two endpoints, the lower first.
type flowKey [2]endpoint

// segment is one TCP segment's data in a direction.
type segment struct {
	// rel is its first byte's sequence number from the direction's base.
	rel uint32
	// data is its bytes as captured; wire its length on the wire, which a
	// capture cut at its snapshot length holds fewer of.
	data []byte
	wire uint64
	// entry is its packet's index in the flow's entries.
	entry int
}

// direction is one way of a flow: the sequence number its data starts
// from and its segments.
type direction struct {
	base    uint32
	baseSet bool
	segs    []segment
}

// entry is one packet of a flow, counted once the flow has been read.
type entry struct {
	pair  *Pair
	proto string
	bytes int
	kind  kind
}

// flow is one TCP connection.
type flow struct {
	key  flowKey
	dirs [2]direction
	// client is the index in key of the side that sent the first SYN, or
	// -1 when no SYN was seen.
	client  int
	entries []entry
	hasData bool
	grpc    bool
	creds   map[string]bool
}

// maxFlowCredentials bounds the credential header names kept for one flow.
const maxFlowCredentials = 32

// sawGRPC implements headerSink.
func (f *flow) sawGRPC() { f.grpc = true }

// sawCredential implements headerSink.
func (f *flow) sawCredential(name string) {
	if f.creds == nil {
		f.creds = map[string]bool{}
	}
	if len(f.creds) < maxFlowCredentials {
		f.creds[name] = true
	}
}

// tracker holds a read's TCP flows.
type tracker struct {
	current map[flowKey]*flow
	all     []*flow
}

// newTracker is an empty tracker.
func newTracker() *tracker {
	return &tracker{current: map[flowKey]*flow{}}
}

// segment keeps pkt, a TCP segment counted under pr, for its flow; false
// when its TCP header cannot be read, so it is counted by its port's name
// now.
func (t *tracker) segment(pr *Pair, pkt ipPacket, original int) bool {
	tcp := pkt.payload
	if len(tcp) < 20 {
		return false
	}
	off := int(tcp[12]>>4) * 4
	if off < 20 || off > len(tcp) {
		return false
	}
	src := endpoint{pkt.src, binary.BigEndian.Uint16(tcp)}
	dst := endpoint{pkt.dst, binary.BigEndian.Uint16(tcp[2:])}
	seq := binary.BigEndian.Uint32(tcp[4:])
	flags := tcp[13]
	syn, ack := flags&tcpSYN != 0, flags&tcpACK != 0
	key, dir := flowKey{src, dst}, 0
	if dst.less(src) {
		key, dir = flowKey{dst, src}, 1
	}
	f := t.current[key]
	// A SYN on a flow that already carried data is the four-tuple used
	// again: a new connection.
	if f == nil || (syn && !ack && f.hasData) {
		f = &flow{key: key, client: -1}
		t.current[key] = f
		t.all = append(t.all, f)
	}
	d := &f.dirs[dir]
	if syn {
		// The SYN takes one sequence number; data starts after it.
		seq++
		d.base, d.baseSet = seq, true
		switch {
		case !ack:
			f.client = dir
		case f.client < 0:
			f.client = 1 - dir
		}
	}
	if !d.baseSet {
		d.base, d.baseSet = seq, true
	}
	f.entries = append(f.entries, entry{pair: pr, proto: pkt.proto, bytes: original})
	data := tcp[off:]
	// The data's length on the wire, by the IP header: never less than what
	// was captured, and at most 65535, an IP header's 16 bits.
	wire := uint64(len(data))
	if n := pkt.wire - off; n >= 0 && n <= 0xffff {
		wire = max(wire, uint64(n))
	}
	if wire > 0 {
		d.segs = append(d.segs, segment{rel: seq - d.base, data: data, wire: wire, entry: len(f.entries) - 1})
		f.hasData = true
	}
	return true
}

// resolve reads every flow and counts its packets into s.
func (t *tracker) resolve(s *Summary) {
	for _, f := range t.all {
		if fl, ok := f.read(); ok {
			s.Flows = append(s.Flows, fl)
			s.CleartextCredentials += len(fl.CleartextCredentials)
			if fl.CleartextManagement {
				s.CleartextManagement++
			}
		}
		f.count(s)
	}
	sortFlows(s.Flows)
	t.all, t.current = nil, nil
}

// sortFlows puts flows carrying a credential in the clear first, then the
// other cleartext management flows, then most packets first.
func sortFlows(flows []Flow) {
	rank := func(f Flow) int {
		switch {
		case len(f.CleartextCredentials) > 0:
			return 0
		case f.CleartextManagement:
			return 1
		}
		return 2
	}
	sort.SliceStable(flows, func(i, j int) bool {
		a, b := flows[i], flows[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if a.Packets != b.Packets {
			return a.Packets > b.Packets
		}
		if a.Client != b.Client {
			return a.Client < b.Client
		}
		return a.Server < b.Server
	})
}
