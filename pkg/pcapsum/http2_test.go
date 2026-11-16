// Tests of HTTP/2 in the clear written by golang.org/x/net/http2's own
// framer and HPACK encoder: every way a header block is carried (padded,
// with priority, across CONTINUATION frames, in a PUSH_PROMISE), a header
// marked never-indexed, and a header name no terminal should be handed.
package pcapsum_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pcapsum"
)

// h2Writer is one side of an HTTP/2 connection written by the real framer.
type h2Writer struct {
	buf bytes.Buffer
	fr  *http2.Framer
	hb  bytes.Buffer
	enc *hpack.Encoder
}

// newH2Writer is a side ready to write frames.
func newH2Writer() *h2Writer {
	w := &h2Writer{}
	w.fr = http2.NewFramer(&w.buf, nil)
	w.enc = hpack.NewEncoder(&w.hb)
	return w
}

// block is fields as one HPACK header block, encoded in turn.
func (w *h2Writer) block(t *testing.T, fields ...hpack.HeaderField) []byte {
	t.Helper()
	w.hb.Reset()
	for _, f := range fields {
		if err := w.enc.WriteField(f); err != nil {
			t.Fatal(err)
		}
	}
	return append([]byte(nil), w.hb.Bytes()...)
}

// TestRead_HTTP2InTheClearIsNamedAndItsCredentialsFlagged: a connection
// opening with HTTP/2's preface is http2, cleartext management, and each
// header carrying a credential is flagged by name however its block was
// carried. A name that is not an HTTP token is reported as a placeholder.
func TestRead_HTTP2InTheClearIsNamedAndItsCredentialsFlagged(t *testing.T) {
	bearer, session, odd := throwaway(t), throwaway(t), throwaway(t)
	c, s := newH2Writer(), newH2Writer()
	c.buf.WriteString(http2.ClientPreface)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.fr.WriteSettings())
	must(s.fr.WriteSettings())
	req := c.block(t,
		hpack.HeaderField{Name: ":method", Value: "GET"},
		hpack.HeaderField{Name: ":path", Value: "/restconf/data"},
		hpack.HeaderField{Name: "content-type", Value: "application/yang-data+json"},
		hpack.HeaderField{Name: "authorization", Value: "Bearer " + bearer},
		hpack.HeaderField{Name: "x-session", Value: session, Sensitive: true},
		hpack.HeaderField{Name: "x-token\x1b[2J", Value: odd},
	)
	// The block in three: a padded HEADERS frame with priority, then two
	// CONTINUATION frames.
	cut := len(req) / 3
	must(c.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: req[:cut], PadLength: 7, Priority: http2.PriorityParam{StreamDep: 0, Weight: 15}}))
	must(c.fr.WriteContinuation(1, false, req[cut:2*cut]))
	must(c.fr.WriteContinuation(1, true, req[2*cut:]))
	// The same request again: its fields now named by the dynamic table.
	must(c.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 3, BlockFragment: c.block(t,
		hpack.HeaderField{Name: ":method", Value: "GET"},
		hpack.HeaderField{Name: "authorization", Value: "Bearer " + bearer}), EndHeaders: true, EndStream: true}))
	// The server promises a push (its block fills the server's table),
	// then answers from that table.
	must(s.fr.WritePushPromise(http2.PushPromiseParam{StreamID: 1, PromiseID: 2, PadLength: 3, EndHeaders: true, BlockFragment: s.block(t,
		hpack.HeaderField{Name: ":path", Value: "/style.css"}, hpack.HeaderField{Name: "x-served-by", Value: "core1"})}))
	must(s.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: s.block(t,
		hpack.HeaderField{Name: ":status", Value: "200"}, hpack.HeaderField{Name: "x-served-by", Value: "core1"})}))
	must(s.fr.WriteData(1, true, []byte(`{"ietf-interfaces:interfaces":{}}`)))
	data := sessionCapture([]chunk{{up: true, data: c.buf.Bytes()}, {up: false, data: s.buf.Bytes()}}, 40000, 8080)
	sum := read(t, data)
	f := onlyFlow(t, sum)
	if f.Protocol != pcapsum.HTTP2 || !f.CleartextManagement || f.Cleartext != sum.Packets || f.Client != "192.0.2.10:40000" {
		t.Errorf("flow = %+v", f)
	}
	want := []string{"(header name not printable)", "authorization", "x-session"}
	if !slices.Equal(f.CleartextCredentials, want) || sum.CleartextCredentials != 3 {
		t.Errorf("credentials = %v (%d), want %v", f.CleartextCredentials, sum.CleartextCredentials, want)
	}
	assertNoValue(t, sum, bearer, session, odd, "\x1b")
}

// TestRead_ANameIsReportedOnlyWhenItCarriedAValue: an empty credential
// header carries nothing, and an ordinary header is no credential.
func TestRead_ANameIsReportedOnlyWhenItCarriedAValue(t *testing.T) {
	c := newH2Writer()
	c.buf.WriteString(http2.ClientPreface)
	if err := c.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: c.block(t,
		hpack.HeaderField{Name: "authorization", Value: ""},
		hpack.HeaderField{Name: "user-agent", Value: "grpc-go/1.83.2"},
		hpack.HeaderField{Name: strings.Repeat("password", 40), Value: "x"},
		hpack.HeaderField{Name: "content-type", Value: "application/grpc+proto"})}); err != nil {
		t.Fatal(err)
	}
	f := onlyFlow(t, read(t, sessionCapture([]chunk{{up: true, data: c.buf.Bytes()}}, 40000, 50001)))
	if f.Protocol != pcapsum.GRPC || len(f.CleartextCredentials) != 0 {
		t.Errorf("flow = %+v", f)
	}
}
