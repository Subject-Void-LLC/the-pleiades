// Fuzz tests of the flow parsers, fed arbitrary bytes in arbitrary pieces
// with arbitrary gaps: none panics, every byte fed is judged some kind, and
// what HTTP/2's parser copies stays inside its bounds. FuzzHeaderBlock gives
// the HPACK decoder arbitrary header blocks behind a real frame header.
package pcapsum

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// recordingSink is a headerSink keeping what it was told.
type recordingSink struct {
	grpc  bool
	names []string
}

// sawGRPC implements headerSink.
func (r *recordingSink) sawGRPC() { r.grpc = true }

// sawCredential implements headerSink.
func (r *recordingSink) sawCredential(name string) { r.names = append(r.names, name) }

// sshSeed is an SSH banner and one KEXINIT-shaped packet in the clear, then
// NEWKEYS, then bytes under the new keys.
func sshSeed() []byte {
	b := []byte("SSH-2.0-OpenSSH_9.6\r\n")
	kexinit := append([]byte{0, 0, 0, 28, 6, 20}, make([]byte, 26)...)
	newkeys := []byte{0, 0, 0, 12, 10, 21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	return append(append(append(b, kexinit...), newkeys...), bytes.Repeat([]byte{0xa5}, 64)...)
}

// tlsSeed is a ClientHello-shaped record, a change cipher spec, and an
// application data record.
func tlsSeed() []byte {
	b := []byte{0x16, 3, 1, 0, 8, 1, 0, 0, 4, 3, 3, 0, 0}
	b = append(b, 0x14, 3, 3, 0, 1, 1)
	return append(append(b, 0x17, 3, 3, 0, 20), make([]byte, 20)...)
}

// h2Seed is a client preface and a HEADERS frame carrying a gRPC
// content-type and a password, as the real framer writes them.
func h2Seed(t testing.TB) []byte {
	var hb, out bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	for _, f := range []hpack.HeaderField{{Name: ":path", Value: "/gnmi.gNMI/Get"}, {Name: "content-type", Value: "application/grpc"}, {Name: "password", Value: "x"}} {
		if err := enc.WriteField(f); err != nil {
			t.Fatal(err)
		}
	}
	out.WriteString(h2Preface)
	fr := http2.NewFramer(&out, nil)
	if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true, PadLength: 2}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// pieces feeds data to p in pieces whose sizes and gaps cuts chooses.
func pieces(t *testing.T, p parser, data, cuts []byte) {
	for i := 0; len(data) > 0; i++ {
		n := len(data)
		if len(cuts) > 0 {
			n = min(len(data), 1+int(cuts[i%len(cuts)])%97)
			if cuts[i%len(cuts)]&0x80 != 0 {
				p.gap(uint64(cuts[(i+1)%len(cuts)]))
			}
		}
		if k := p.feed(data[:n], i%2 == 0); k == kindNone {
			t.Fatalf("%T judged %d bytes as nothing", p, n)
		}
		if h, ok := p.(*h2Parser); ok && (len(h.frame) > maxHeaderBlock || len(h.block) > maxHeaderBlock) {
			t.Fatalf("http2 copied %d frame and %d block bytes", len(h.frame), len(h.block))
		}
		data = data[n:]
	}
}

// FuzzParsers: every parser reads any bytes, cut and gapped anyhow.
func FuzzParsers(f *testing.F) {
	f.Add(sshSeed(), []byte{3, 200, 17})
	f.Add(tlsSeed(), []byte{1, 2, 3, 4, 5})
	f.Add(h2Seed(f), []byte{9, 9, 130, 1})
	f.Add([]byte(h2Preface+"\x00\x00\x05\x01\x05\x00\x00\x00\x01\x82\x86\x84\x41\x8a"), []byte{})
	f.Fuzz(func(t *testing.T, data, cuts []byte) {
		for _, p := range []parser{&sshParser{}, &tlsParser{}, newH2Parser(true, &recordingSink{}), newH2Parser(false, &recordingSink{}), plain{}} {
			pieces(t, p, data, cuts)
		}
		_ = open(data)
	})
}

// FuzzHeaderBlock: any bytes as a header block behind a real HEADERS frame
// header decode or are refused without panicking, and a name the sink is
// told is a printable header name or the placeholder.
func FuzzHeaderBlock(f *testing.F) {
	seed := h2Seed(f)
	f.Add(seed[len(h2Preface)+9:], byte(h2EndHeaders|h2Padded))
	f.Add([]byte{0x3f, 0xe1, 0x1f, 0x82}, byte(h2EndHeaders))
	f.Fuzz(func(t *testing.T, block []byte, flags byte) {
		if len(block) > 1<<16 {
			return
		}
		sink := &recordingSink{}
		h := newH2Parser(false, sink)
		hdr := make([]byte, 9)
		hdr[0], hdr[1], hdr[2] = byte(len(block)>>16), byte(len(block)>>8), byte(len(block))
		hdr[3], hdr[4] = h2Headers, flags
		binary.BigEndian.PutUint32(hdr[5:], 1)
		for range 2 {
			// Twice, so a table the first block filled is read by the second.
			h.feed(append(hdr, block...), true)
		}
		for _, n := range sink.names {
			if n != unprintableHeader && headerName(n) != n {
				t.Fatalf("an unprintable name was reported: %q", n)
			}
		}
	})
}
