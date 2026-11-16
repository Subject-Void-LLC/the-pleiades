// Tests of what a review of the flow reading found: HTTP/1.x headers
// searched by name (through a real net/http session), a repeated segment
// with different bytes, a packet whose data the capture cut away, bytes
// framed as encrypted that read as text, and each way header searching
// gives up being counted.
package pcapsum_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
)

// httpSession is a real HTTP/1.1 keep-alive connection through net/http:
// a GET with Basic authentication answered with a cookie and a chunked
// body, then a POST with a token header and a body of known length.
func httpSession(t *testing.T, user, pass, token string) []chunk {
	t.Helper()
	l := listen(t)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: token})
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(`{"ietf-interfaces:interfaces":{}}`))
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	dialed := make(chan *recorder, 1)
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		rec := &recorder{Conn: raw}
		select {
		case dialed <- rec:
		default:
		}
		return rec, nil
	}}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	get, err := http.NewRequest(http.MethodGet, "http://"+l.Addr().String()+"/restconf/data", nil)
	if err != nil {
		t.Fatal(err)
	}
	get.SetBasicAuth(user, pass)
	post, err := http.NewRequest(http.MethodPost, "http://"+l.Addr().String()+"/restconf/data", strings.NewReader(`{"interface":"lo42"}`))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("X-Auth-Token", token)
	for _, req := range []*http.Request{get, post} {
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	select {
	case rec := <-dialed:
		return rec.recorded()
	default:
		t.Fatal("the client never dialed")
	}
	return nil
}

// TestRead_HTTP1HeadersAreSearchedByName: a real HTTP/1.1 connection is
// http, cleartext management, and its Authorization, Set-Cookie and
// X-Auth-Token headers are flagged by name across both requests, a chunked
// body and a body of known length; no value is anywhere in the summary.
func TestRead_HTTP1HeadersAreSearchedByName(t *testing.T) {
	user, pass, token := throwaway(t), throwaway(t), throwaway(t)
	s := read(t, sessionCapture(httpSession(t, user, pass, token), 40000, 80))
	f := onlyFlow(t, s)
	want := []string{"authorization", "set-cookie", "x-auth-token"}
	if f.Protocol != pcapsum.HTTP || !f.CleartextManagement || f.Cleartext != s.Packets || !slices.Equal(f.CleartextCredentials, want) || f.HeadersUnread != 0 {
		t.Errorf("flow = %+v", f)
	}
	assertNoValue(t, s, user, pass, token)
}

// TestRead_HTTP1GivingUpIsCounted: a protocol switch (101), a line over
// 8 KiB and a lost segment each leave headers unsearched, and say so.
func TestRead_HTTP1GivingUpIsCounted(t *testing.T) {
	for name, c := range map[string][]chunk{
		"switching protocols": {
			{up: true, data: []byte("GET / HTTP/1.1\r\nUpgrade: h2c\r\n\r\n")},
			{up: false, data: []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: h2c\r\n\r\nanything at all")},
		},
		"a long line":        {{up: true, data: []byte("GET / HTTP/1.1\r\nX-Big: " + strings.Repeat("a", 9000) + "\r\n\r\n")}},
		"not http after all": {{up: true, data: []byte("GET / HTTP/1.1\r\n\r\nthis is no request line\r\n")}},
	} {
		if f := onlyFlow(t, read(t, sessionCapture(c, 40000, 80))); f.HeadersUnread != 1 || f.Protocol != pcapsum.HTTP {
			t.Errorf("%s: %+v", name, f)
		}
	}
	// A lost segment in the headers of a long request.
	segs := flowPackets([]chunk{{up: true, data: []byte("GET / HTTP/1.1\r\nX-Pad: " + strings.Repeat("p", 4000) + "\r\nAuthorization: Basic x\r\n\r\n")}})
	data := dataIndexes(segs, true)
	lost := append(append([]pkt(nil), segs[:data[1]]...), segs[data[1]+1:]...)
	if f := onlyFlow(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(lost, 40000, 80)))); f.HeadersUnread != 1 || len(f.CleartextCredentials) != 0 {
		t.Errorf("a lost header segment: %+v", f)
	}
}

// tlsAppSegment is the index in segs of a server data segment that lies
// inside one application data record.
func tlsAppSegment(t *testing.T, segs []pkt) int {
	t.Helper()
	within, _ := recordInside(segs)
	if len(within) < 3 {
		t.Fatalf("no segment inside a record: %v", within)
	}
	return within[len(within)/2]
}

// TestRead_ARepeatedSegmentIsItsOwnBytesKind: a retransmission with the same
// bytes keeps the kind of what it repeats; one, or an overlap, with other
// bytes is cleartext, since nothing judged those bytes.
func TestRead_ARepeatedSegmentIsItsOwnBytesKind(t *testing.T) {
	segs := flowPackets(tlsSession(t, 0x0304, bytes.Repeat([]byte("r"), 64<<10)))
	i := tlsAppSegment(t, segs)
	same := append(append(append([]pkt(nil), segs[:i+1]...), segs[i]), segs[i+1:]...)
	assertSealed(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(same, 40000, 443))), pcapsum.TLS, 443)

	forged := segs[i]
	forged.data = []byte(strings.Repeat("plaintext ", len(segs[i].data)/10+1))[:len(segs[i].data)]
	overlap := segs[i]
	overlap.seq += 50
	overlap.data = []byte(strings.Repeat("x", 50))
	for name, extra := range map[string]pkt{"other bytes": forged, "an overlap with other bytes": overlap} {
		changed := append(append(append([]pkt(nil), segs[:i+1]...), extra), segs[i+1:]...)
		s := read(t, pcapFile(binary.LittleEndian, tcpFrames(changed, 40000, 443)))
		if f := onlyFlow(t, s); f.Cleartext != 1 {
			t.Errorf("%s: %+v", name, f)
		}
	}
}

// TestRead_APacketWithNoCapturedDataIsCleartext: a data packet the
// snapshot length cut down to its headers was seen to carry nothing, so it
// is cleartext, in TLS application data and in SSH after NEWKEYS alike.
func TestRead_APacketWithNoCapturedDataIsCleartext(t *testing.T) {
	segs := flowPackets(tlsSession(t, 0x0304, bytes.Repeat([]byte("s"), 64<<10)))
	i := tlsAppSegment(t, segs)
	// Ethernet, IPv4 and TCP: 54 bytes, no data.
	segs[i].snap = 54
	if f := onlyFlow(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(segs, 40000, 443)))); f.Cleartext != 1 || f.Encrypted == 0 {
		t.Errorf("TLS: %+v", f)
	}
	ssh := flowPackets(sshSession(t, "aes128-gcm@openssh.com"))
	up := dataIndexes(ssh, true)
	ssh[up[len(up)-1]].snap = 54
	if f := onlyFlow(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(ssh, 40000, 22)))); f.Cleartext != 1 {
		t.Errorf("SSH: %+v", f)
	}
}

// assertText: s's one flow had bytes framed as encrypted that read as text,
// so none of its packets is encrypted.
func assertText(t *testing.T, name string, s pcapsum.Summary) {
	t.Helper()
	f := onlyFlow(t, s)
	if !f.CleartextInEncrypted || f.Encrypted != 0 || s.Encrypted != 0 || s.CleartextInEncrypted != 1 || f.Cleartext == 0 {
		t.Errorf("%s: %+v", name, f)
	}
}

// TestRead_TextFramedAsEncryptedIsCleartext: a forged ClientHello and then
// an application data record of 16 KiB of text, an SSH session whose bytes
// after NEWKEYS are text (the none cipher), and a text record a lost TLS
// direction resynchronized on are all cleartext and flagged.
func TestRead_TextFramedAsEncryptedIsCleartext(t *testing.T) {
	forged := append([]byte{0x16, 3, 1, 0, 4, 1, 0, 0, 0, 0x17, 3, 3, 0x40, 0}, bytes.Repeat([]byte("show running-config\n"), 820)[:16384]...)
	assertText(t, "a forged record", read(t, sessionCapture([]chunk{{up: true, data: forged}}, 40000, 443)))

	kexinit := append([]byte{0, 0, 0, 12, 10, 20}, make([]byte, 10)...)
	newkeys := []byte{0, 0, 0, 12, 10, 21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	none := append(append(append([]byte("SSH-2.0-OpenSSH_9.6 hpn\r\n"), kexinit...), newkeys...), bytes.Repeat([]byte("\x00\x00\x00\x2c\x0a\x5eterminal output line\r\n"), 40)...)
	assertText(t, "the none cipher", read(t, sessionCapture([]chunk{{up: false, data: none}}, 40000, 22)))

	// A ClientHello, a record whose header segment is lost, then a fresh
	// segment that begins with a record header around text.
	text := append([]byte{0x17, 3, 3, 0x03, 0xe8}, bytes.Repeat([]byte("interface lo42\n"), 67)[:1000]...)
	chunks := []chunk{
		{up: true, data: []byte{0x16, 3, 1, 0, 4, 1, 0, 0, 0}},
		{up: false, data: []byte{0x16, 3, 3, 0, 4, 2, 0, 0, 0}},
		{up: true, data: append([]byte{0x17, 3, 3, 0x07, 0xd0}, noise(t, 2000)...)},
		{up: false, data: append([]byte{0x17, 3, 3, 0, 100}, noise(t, 100)...)},
		{up: true, data: text},
	}
	segs := flowPackets(chunks)
	up := dataIndexes(segs, true)
	lost := append(append([]pkt(nil), segs[:up[1]]...), segs[up[1]+1:]...)
	s := read(t, pcapFile(binary.LittleEndian, tcpFrames(lost, 40000, 443)))
	if f := onlyFlow(t, s); !f.CleartextInEncrypted || s.CleartextInEncrypted != 1 {
		t.Errorf("text after a resync: %+v", f)
	}
}

// printableShare is the share of p that is printable ASCII.
func printableShare(p []byte) float64 {
	n := 0
	for _, b := range p {
		if (b >= 0x20 && b < 0x7f) || b == '\t' || b == '\n' || b == '\r' {
			n++
		}
	}
	return float64(n) / float64(len(p))
}

// oneWay is everything one side of chunks sent.
func oneWay(chunks []chunk, up bool) []byte {
	var out []byte
	for _, c := range chunks {
		if c.up == up {
			out = append(out, c.data...)
		}
	}
	return out
}

// TestText_RealSessionsAreNeverText: the most printable application data
// record of 64 bytes or more in real TLS sessions, and the most printable
// 256-byte block after NEWKEYS in real SSH sessions, stay far under the 3/4
// that would call them text, and every such session reads as not text.
func TestText_RealSessionsAreNeverText(t *testing.T) {
	worst := 0.0
	for _, v := range []uint16{0x0303, 0x0304} {
		for _, size := range []int{1, 100, 1000, 20000} {
			chunks := tlsSession(t, v, bytes.Repeat([]byte("a"), size))
			for _, up := range []bool{true, false} {
				stream := oneWay(chunks, up)
				for off := 0; off+5 <= len(stream); off += 5 + int(binary.BigEndian.Uint16(stream[off+3:])) {
					n := int(binary.BigEndian.Uint16(stream[off+3:]))
					if stream[off] == 0x17 && n >= 64 && off+5+n <= len(stream) {
						worst = max(worst, printableShare(stream[off+5:off+5+n]))
					}
				}
			}
			if f := onlyFlow(t, read(t, sessionCapture(chunks, 40000, 443))); f.CleartextInEncrypted {
				t.Errorf("TLS %x of %d bytes read as text", v, size)
			}
		}
	}
	for _, cipher := range []string{"chacha20-poly1305@openssh.com", "aes128-gcm@openssh.com", "aes128-ctr"} {
		chunks := sshSession(t, cipher)
		for _, up := range []bool{true, false} {
			stream := oneWay(chunks, up)
			// Past the banner, packets in the clear up to NEWKEYS (21).
			off := bytes.IndexByte(stream, '\n') + 1
			for off+6 <= len(stream) {
				n := int(binary.BigEndian.Uint32(stream[off:]))
				typ := stream[off+5]
				off += 4 + n
				if typ == 21 {
					break
				}
			}
			for b := off; b+256 <= len(stream); b += 256 {
				worst = max(worst, printableShare(stream[b:b+256]))
			}
		}
		if f := onlyFlow(t, read(t, sessionCapture(chunks, 40000, 22))); f.CleartextInEncrypted {
			t.Errorf("SSH %s read as text", cipher)
		}
	}
	t.Logf("the most printable run of real ciphertext: %.0f%%", worst*100)
	if worst >= 0.6 {
		t.Errorf("real ciphertext came within reach of the text bound: %.2f", worst)
	}
}

// h2Flow is the one flow of a capture of an HTTP/2 client sending frames.
func h2Flow(t *testing.T, frames []byte) pcapsum.Flow {
	t.Helper()
	return onlyFlow(t, read(t, sessionCapture([]chunk{{up: true, data: append([]byte(http2.ClientPreface), frames...)}}, 40000, 8080)))
}

// TestRead_HTTP2GivingUpIsCounted: a block over 64 KiB, a block that will
// not decode (and the one after it), a name over 256 bytes, a credential
// name past the 32 kept, and a lost segment each count in headers_unread.
func TestRead_HTTP2GivingUpIsCounted(t *testing.T) {
	block := func(fields ...hpack.HeaderField) []byte {
		var b bytes.Buffer
		enc := hpack.NewEncoder(&b)
		for _, f := range fields {
			if err := enc.WriteField(f); err != nil {
				t.Fatal(err)
			}
		}
		return b.Bytes()
	}
	frames := func(write func(fr *http2.Framer) error) []byte {
		var b bytes.Buffer
		if err := write(http2.NewFramer(&b, nil)); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	big := frames(func(fr *http2.Framer) error {
		piece := make([]byte, 16000)
		if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: piece}); err != nil {
			return err
		}
		for i := range 4 {
			if err := fr.WriteContinuation(1, i == 3, piece); err != nil {
				return err
			}
		}
		return nil
	})
	if f := h2Flow(t, big); f.HeadersUnread != 1 {
		t.Errorf("a block over 64 KiB: %+v", f)
	}
	broken := frames(func(fr *http2.Framer) error {
		// Index 70 names no table entry: the block is refused, the table
		// lost, and the next block unread too.
		if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: []byte{0x80 | 70}}); err != nil {
			return err
		}
		return fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 3, EndHeaders: true, BlockFragment: block(hpack.HeaderField{Name: "authorization", Value: "x"})})
	})
	if f := h2Flow(t, broken); f.HeadersUnread != 2 || len(f.CleartextCredentials) != 0 {
		t.Errorf("an undecodable block: %+v", f)
	}
	long := frames(func(fr *http2.Framer) error {
		return fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: block(hpack.HeaderField{Name: "x-" + strings.Repeat("a", 300), Value: "v"})})
	})
	if f := h2Flow(t, long); f.HeadersUnread != 1 {
		t.Errorf("a name over 256 bytes: %+v", f)
	}
	many := frames(func(fr *http2.Framer) error {
		var fields []hpack.HeaderField
		for i := range 40 {
			fields = append(fields, hpack.HeaderField{Name: fmt.Sprintf("x-token-%d", i), Value: "v"})
		}
		return fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: block(fields...)})
	})
	if f := h2Flow(t, many); f.HeadersUnread != 8 || len(f.CleartextCredentials) != 32 {
		t.Errorf("forty credential names: %d kept, %d unread", len(f.CleartextCredentials), f.HeadersUnread)
	}
	// A lost segment crossing frame headers: the framing is lost.
	pad := frames(func(fr *http2.Framer) error {
		for i := range 4 {
			if err := fr.WriteData(1, false, make([]byte, 1000)); err != nil {
				return err
			}
			if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: uint32(3 + 2*i), EndHeaders: true, BlockFragment: block(hpack.HeaderField{Name: ":path", Value: "/"})}); err != nil {
				return err
			}
		}
		return nil
	})
	segs := flowPackets([]chunk{{up: true, data: append([]byte(http2.ClientPreface), pad...)}})
	up := dataIndexes(segs, true)
	lost := append(append([]pkt(nil), segs[:up[1]]...), segs[up[1]+1:]...)
	if f := onlyFlow(t, read(t, pcapFile(binary.LittleEndian, tcpFrames(lost, 40000, 8080)))); f.HeadersUnread != 1 {
		t.Errorf("a lost segment: %+v", f)
	}
}
