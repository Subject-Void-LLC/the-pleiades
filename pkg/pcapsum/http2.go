// Package pcapsum: HTTP/2 in the clear, read frame by frame so that each
// header block can be decoded (HPACK, RFC 7541) in the order it was sent,
// the dynamic table kept as the peer's encoder kept it. A request saying
// content-type application/grpc names the flow grpc, and a header that
// carries a credential is reported by its name. A header's value is looked
// at only to compare it, and never kept.
package pcapsum

import (
	"strings"

	"golang.org/x/net/http2/hpack"
)

// The HTTP/2 frame types that carry a header block, and the flags read.
const (
	h2Headers      = 0x1
	h2PushPromise  = 0x5
	h2Continuation = 0x9
	h2EndHeaders   = 0x4
	h2Padded       = 0x8
	h2Priority     = 0x20
)

// The bounds on what decoding copies and keeps: a header block (all its
// frames), one header's name or value, and the dynamic table the peer may
// ask for. A block past its bound stops this direction's decoding, since
// the table can no longer be followed; framing goes on.
const (
	maxHeaderBlock  = 64 << 10
	maxHeaderString = 16 << 10
	maxHeaderTable  = 64 << 10
)

// headerSink is told what a direction's header blocks held: a gRPC
// content-type, and the name of each header that carried a credential.
type headerSink interface {
	sawGRPC()
	sawCredential(name string)
}

// h2Parser reads one direction of HTTP/2 in the clear. Every byte is
// cleartext; the parsing is for naming the flow and finding credentials.
type h2Parser struct {
	// preface is the client preface's bytes still to come (0 for a server).
	preface int
	hdr     [9]byte
	hdrN    int
	// remaining is the frame's payload not yet seen; typ and flags are the
	// frame's.
	remaining   uint64
	typ, flags  byte
	collect     bool
	frame       []byte
	block       []byte
	dec         *hpack.Decoder
	undecodable bool
	lost        bool
	sink        headerSink
}

// newH2Parser is a parser for one direction, the client's when client is
// set, telling sink what its header blocks held.
func newH2Parser(client bool, sink headerSink) *h2Parser {
	h := &h2Parser{sink: sink}
	if client {
		h.preface = len(h2Preface)
	}
	h.dec = hpack.NewDecoder(4096, h.field)
	h.dec.SetAllowedMaxDynamicTableSize(maxHeaderTable)
	h.dec.SetMaxStringLength(maxHeaderString)
	return h
}

// field is told each decoded header. The value is compared, never kept.
func (h *h2Parser) field(f hpack.HeaderField) {
	if f.Name == "content-type" && strings.HasPrefix(f.Value, "application/grpc") {
		h.sink.sawGRPC()
	}
	if f.Value != "" && (f.Sensitive || credentialHeader(f.Name)) {
		h.sink.sawCredential(headerName(f.Name))
	}
}

// feed implements parser.
func (h *h2Parser) feed(p []byte, _ bool) kind {
	for len(p) > 0 && !h.lost {
		if h.preface > 0 {
			n := min(len(p), h.preface)
			h.preface -= n
			p = p[n:]
			continue
		}
		if h.hdrN < len(h.hdr) {
			n := copy(h.hdr[h.hdrN:], p)
			h.hdrN += n
			p = p[n:]
			if h.hdrN == len(h.hdr) {
				h.begin()
			}
			continue
		}
		n := min(uint64(len(p)), h.remaining)
		if h.collect {
			h.frame = append(h.frame, p[:n]...)
		}
		h.remaining -= n
		p = p[n:]
		if h.remaining == 0 {
			h.end()
		}
	}
	return kindCleartext
}

// begin reads a frame header from hdr.
func (h *h2Parser) begin() {
	length := uint64(h.hdr[0])<<16 | uint64(h.hdr[1])<<8 | uint64(h.hdr[2])
	h.typ, h.flags, h.remaining = h.hdr[3], h.hdr[4], length
	carries := h.typ == h2Headers || h.typ == h2PushPromise || h.typ == h2Continuation
	h.collect = carries && !h.undecodable
	if h.collect && length > maxHeaderBlock {
		h.collect, h.undecodable = false, true
	}
	h.frame = h.frame[:0]
	if h.remaining == 0 {
		h.end()
	}
}

// end ends a frame: a header-carrying one adds its fragment to the block,
// and the last of a block decodes it.
func (h *h2Parser) end() {
	h.hdrN = 0
	if !h.collect {
		return
	}
	h.collect = false
	fragment, ok := h.fragment()
	if !ok || len(h.block)+len(fragment) > maxHeaderBlock {
		h.undecodable = true
		return
	}
	h.block = append(h.block, fragment...)
	if h.flags&h2EndHeaders == 0 {
		return
	}
	_, err := h.dec.Write(h.block)
	if err == nil {
		err = h.dec.Close()
	}
	h.block = h.block[:0]
	if err != nil {
		h.undecodable = true
	}
}

// fragment is the header block fragment in the frame just read, past its
// padding length, priority or promised stream, and without its padding.
func (h *h2Parser) fragment() ([]byte, bool) {
	f := h.frame
	if h.typ == h2Continuation {
		return f, true
	}
	pad := 0
	if h.flags&h2Padded != 0 {
		if len(f) < 1 {
			return nil, false
		}
		pad, f = int(f[0]), f[1:]
	}
	skip := 0
	switch {
	case h.typ == h2PushPromise:
		skip = 4
	case h.flags&h2Priority != 0:
		skip = 5
	}
	if len(f) < skip+pad {
		return nil, false
	}
	return f[skip : len(f)-pad], true
}

// gap implements parser: a gap in a frame that carries no header block is
// skipped; in one that does, decoding stops (the table is lost) and framing
// goes on; anywhere else the framing is lost.
func (h *h2Parser) gap(n uint64) {
	if h.lost {
		return
	}
	if h.preface == 0 && h.hdrN == len(h.hdr) && n <= h.remaining {
		if h.collect {
			h.collect, h.undecodable = false, true
		}
		h.remaining -= n
		if h.remaining == 0 {
			h.end()
		}
		return
	}
	h.lost = true
}

// credentialNames are header names that carry a credential, exactly.
var credentialNames = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"username":            true,
	"user":                true,
	"cookie":              true,
	"set-cookie":          true,
}

// credentialWords are words whose presence in a header's name says it
// carries a credential: password, x-api-key, x-auth-token, secret-bin.
var credentialWords = []string{"password", "passwd", "secret", "token", "apikey", "api-key", "api_key", "credential"}

// maxCredentialName is the longest header name searched for a credential's
// word. No client names a header longer, and the bound keeps a hostile
// block of many long names from costing a search of each.
const maxCredentialName = 256

// credentialHeader reports whether a header of this name carries a
// credential.
func credentialHeader(name string) bool {
	if len(name) > maxCredentialName {
		return false
	}
	name = strings.ToLower(name)
	if credentialNames[name] {
		return true
	}
	for _, w := range credentialWords {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

// unprintableHeader stands for a header name that is not an HTTP token, so
// that nothing a capture holds reaches a terminal as it was written.
const unprintableHeader = "(header name not printable)"

// headerName is name as it may be reported: an HTTP/2 header name (lower
// case letters, digits and - _ . !), at most 64 bytes, or unprintableHeader.
func headerName(name string) string {
	if name == "" || len(name) > 64 {
		return unprintableHeader
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && !strings.ContainsRune("-_.!", rune(c)) {
			return unprintableHeader
		}
	}
	return name
}
