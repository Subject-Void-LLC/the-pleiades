// Package pcapsum: reading a flow once the capture is read. Each direction
// is put in sequence order, its first bytes choose its parser, and each
// segment's bytes are fed to it, a gap where the capture lacks bytes, so
// every packet is judged by what its own bytes were.
package pcapsum

import (
	"net/netip"
	"sort"
)

// read reads f's directions and judges each of its packets; it returns the
// Flow it reports and true when its bytes named it.
func (f *flow) read() (Flow, bool) {
	for i := range f.dirs {
		segs := f.dirs[i].segs
		sort.SliceStable(segs, func(a, b int) bool { return segs[a].rel < segs[b].rel })
	}
	opens := [2]opening{f.dirs[0].opening(), f.dirs[1].opening()}
	var parsers [2]parser
	name := ""
	h2 := -1
	for i, o := range opens {
		if o == openH2Client {
			h2 = i
		}
	}
	for i, o := range opens {
		switch {
		case h2 >= 0:
			parsers[i] = newH2Parser(i == h2, f)
			name = HTTP2
		case o == openSSH:
			parsers[i] = &sshParser{}
			name = SSH
		case o == openTLSHello || o == openTLSReply:
			parsers[i] = &tlsParser{}
			if name == "" {
				name = TLS
			}
		default:
			parsers[i] = plain{}
		}
	}
	for i := range f.dirs {
		f.feed(&f.dirs[i], parsers[i])
		f.dirs[i].segs = nil
	}
	if name == HTTP2 && f.grpc {
		name = GRPC
	}
	f.judge(name)
	if name == "" {
		return Flow{}, false
	}
	client := f.clientSide(opens, h2)
	out := Flow{
		Client:   netip.AddrPortFrom(f.key[client].addr, f.key[client].port).String(),
		Server:   netip.AddrPortFrom(f.key[1-client].addr, f.key[1-client].port).String(),
		Protocol: name,
	}
	for _, e := range f.entries {
		out.Packets++
		switch e.kind {
		case kindEncrypted:
			out.Encrypted++
		case kindKeyExchange:
			out.KeyExchange++
		default:
			out.Cleartext++
		}
	}
	out.CleartextManagement = (name == HTTP2 || name == GRPC) && out.Cleartext > 0
	for c := range f.creds {
		out.CleartextCredentials = append(out.CleartextCredentials, c)
	}
	sort.Strings(out.CleartextCredentials)
	return out, true
}

// opening is what d's first bytes, from its base, say it is: plain when the
// capture lacks its first bytes.
func (d *direction) opening() opening {
	if len(d.segs) == 0 || d.segs[0].rel != 0 {
		return openPlain
	}
	var first []byte
	next := uint64(0)
	for _, s := range d.segs {
		start := uint64(s.rel)
		if start > next || len(first) >= openingLen {
			break
		}
		end := start + uint64(len(s.data))
		if end <= next {
			continue
		}
		take := s.data[next-start:]
		take = take[:min(len(take), openingLen-len(first))]
		first = append(first, take...)
		next += uint64(len(take))
		if uint64(len(s.data)) < s.wire {
			// The capture cut this segment short: what follows is not here.
			break
		}
	}
	return open(first)
}

// feed feeds d's segments to p in sequence order, recording the kind of
// each segment's bytes on its packet's entry. A segment wholly seen before
// (a retransmission) is the kind of the segment read just before it in
// sequence order, which is the one it repeats or the one ending inside it.
func (f *flow) feed(d *direction, p parser) {
	var next uint64
	var last kind
	for _, s := range d.segs {
		start := uint64(s.rel)
		end := start + s.wire
		if end <= next {
			f.entries[s.entry].kind |= last
			continue
		}
		var k kind
		from := uint64(0)
		switch {
		case start > next:
			p.gap(start - next)
		case start < next:
			// The part already read is the kind it was.
			k |= last
			from = next - start
		}
		if from < uint64(len(s.data)) {
			k |= p.feed(s.data[from:], from == 0)
		}
		if captured := start + uint64(len(s.data)); captured < end {
			p.gap(end - max(captured, next))
		}
		next = end
		f.entries[s.entry].kind |= k
		if k != kindNone {
			last = k
		}
	}
}

// judge settles each entry's kind and protocol name. A packet whose bytes
// were judged is the kind they were; one that carried none (a SYN, a bare
// ACK, a FIN) is encrypted in a flow that carried encrypted bytes, key
// exchange in one that carried only key exchange, else cleartext.
func (f *flow) judge(name string) {
	var all kind
	for _, e := range f.entries {
		all |= e.kind
	}
	empty := kindCleartext
	switch {
	case all&kindEncrypted != 0:
		empty = kindEncrypted
	case all&kindKeyExchange != 0:
		empty = kindKeyExchange
	}
	for i := range f.entries {
		e := &f.entries[i]
		e.kind = e.kind.final()
		if e.kind == kindNone {
			e.kind = empty
		}
		if name != "" {
			e.proto = name
		}
	}
}

// count counts f's packets into s.
func (f *flow) count(s *Summary) {
	for _, e := range f.entries {
		addKind(s, e.pair, e.proto, e.bytes, e.kind)
	}
	f.entries = nil
}

// clientSide is the index in f.key of the client: the side that sent the
// first SYN, else the side that sent the HTTP/2 preface or a ClientHello (or
// not the ServerHello), else the side with the higher port.
func (f *flow) clientSide(opens [2]opening, h2 int) int {
	switch {
	case f.client >= 0:
		return f.client
	case h2 >= 0:
		return h2
	}
	for i, o := range opens {
		switch o {
		case openTLSHello:
			return i
		case openTLSReply:
			return 1 - i
		}
	}
	if f.key[0].port > f.key[1].port {
		return 0
	}
	return 1
}
