// Package pcapsum: reading a flow once the capture is read. Each direction
// is put in sequence order, its first bytes choose its parser, and each
// segment's bytes are fed to it, a gap where the capture lacks bytes, so
// every packet is judged by what its own bytes were.
package pcapsum

import (
	"bytes"
	"net/netip"
	"sort"
)

// read reads f's directions and judges each of its packets; it returns the
// Flow it reports and true when its bytes named it. opts says which
// servers are management endpoints.
func (f *flow) read(opts Options) (Flow, bool) {
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
			if name == "" || name == HTTP {
				name = TLS
			}
		case o == openH1Request || o == openH1Response:
			parsers[i] = newH1Parser(f)
			if name == "" {
				name = HTTP
			}
		default:
			parsers[i] = plain{}
		}
	}
	for i := range f.dirs {
		f.feed(&f.dirs[i], parsers[i])
		if t, ok := parsers[i].(textual); ok && t.sawText() {
			// Bytes framed as encrypted read as text: none of this
			// direction's packets is proven encrypted.
			f.text = true
			f.unseal(&f.dirs[i])
		}
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
	server := netip.AddrPortFrom(f.key[1-client].addr, f.key[1-client].port)
	out := Flow{
		Client:   netip.AddrPortFrom(f.key[client].addr, f.key[client].port).String(),
		Server:   server.String(),
		Protocol: name,
		First:    f.span.first, Last: f.span.last, DurationSeconds: f.span.seconds(),
	}
	marks := dscpTally{}
	for _, e := range f.entries {
		out.Packets++
		out.Bytes += int64(e.bytes)
		marks.add(e.dscp, e.bytes)
		switch e.kind {
		case kindEncrypted:
			out.Encrypted++
		case kindKeyExchange:
			out.KeyExchange++
		default:
			out.Cleartext++
		}
	}
	out.DSCP = marks.list()
	if name == HTTP || name == HTTP2 || name == GRPC {
		// Only a management endpoint's HTTP is management (management.go).
		out.Management = opts.management(server, f.api)
		out.CleartextManagement = out.Management != "" && out.Cleartext > 0
	}
	out.CleartextInEncrypted = f.text
	out.HeadersUnread = f.unread
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

// span is a stretch of a direction already read: its first and last
// sequence numbers past the base, its bytes as captured, and their kind.
type span struct {
	start, end uint64
	data       []byte
	kind       kind
}

// feed feeds d's segments to p in sequence order, recording the kind of
// each segment's bytes on its packet's entry. A segment repeating bytes
// already read (a retransmission, an overlap) is the kind of the bytes it
// repeats when its bytes are the same, and cleartext when they differ, since
// the parser judged only the first. A data packet whose bytes the capture
// holds none of is cleartext in every state: nothing was seen to be
// encrypted.
func (f *flow) feed(d *direction, p parser) {
	var next uint64
	var read []span
	for _, s := range d.segs {
		start := uint64(s.rel)
		end := start + s.wire
		var k kind
		if start < next {
			k |= repeated(read, start, start, min(end, next), s.data)
		}
		if end > next {
			from := uint64(0)
			if start > next {
				p.gap(start - next)
			} else {
				from = next - start
			}
			if from < uint64(len(s.data)) {
				fed := p.feed(s.data[from:], from == 0)
				read = append(read, span{start: start + from, end: start + uint64(len(s.data)), data: s.data[from:], kind: fed})
				k |= fed
			}
			if captured := start + uint64(len(s.data)); captured < end {
				p.gap(end - max(captured, next))
			}
			next = end
		}
		if len(s.data) == 0 {
			k |= kindCleartext
		}
		f.entries[s.entry].kind |= k
	}
}

// repeated is the kind of data, a segment's captured bytes from sequence
// number start, over [lo, hi), which spans (in order, not overlapping)
// already read: the kinds of the spans it repeats when its bytes are theirs,
// cleartext when any byte differs or was never read (a gap the parser was
// told of).
func repeated(spans []span, start, lo, hi uint64, data []byte) kind {
	hi = min(hi, start+uint64(len(data)))
	if lo >= hi {
		return kindNone
	}
	var k kind
	i := sort.Search(len(spans), func(i int) bool { return spans[i].end > lo })
	for x := lo; x < hi; {
		if i == len(spans) || spans[i].start > x {
			// Bytes the parser never judged.
			return kindCleartext
		}
		sp := spans[i]
		upto := min(hi, sp.end)
		if !bytes.Equal(data[x-start:upto-start], sp.data[x-sp.start:upto-sp.start]) {
			return kindCleartext
		}
		k |= sp.kind
		x = upto
		i++
	}
	return k
}

// unseal makes every packet of d counted encrypted cleartext.
func (f *flow) unseal(d *direction) {
	for _, s := range d.segs {
		e := &f.entries[s.entry]
		if e.kind&kindEncrypted != 0 {
			e.kind = e.kind&^kindEncrypted | kindCleartext
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
// first SYN, else the side that sent the HTTP/2 preface, a ClientHello or an
// HTTP/1.x request (or not the ServerHello or status line), else the side
// with the higher port.
func (f *flow) clientSide(opens [2]opening, h2 int) int {
	switch {
	case f.client >= 0:
		return f.client
	case h2 >= 0:
		return h2
	}
	for i, o := range opens {
		switch o {
		case openTLSHello, openH1Request:
			return i
		case openTLSReply, openH1Response:
			return 1 - i
		}
	}
	if f.key[0].port > f.key[1].port {
		return 0
	}
	return 1
}
