package rfc2217

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// TestIACFilter_PlainDataPassesThroughUnchanged proves the common case.
func TestIACFilter_PlainDataPassesThroughUnchanged(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	data, frames, verbs, err := f.feed(&reply, []byte("line status ok\r\n"))
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if string(data) != "line status ok\r\n" {
		t.Errorf("data = %q, want %q", data, "line status ok\r\n")
	}
	if len(frames) != 0 || len(verbs) != 0 || reply.Len() != 0 {
		t.Errorf("expected no frames/verbs/reply, got frames=%v verbs=%v reply=%v", frames, verbs, reply.Bytes())
	}
}

// TestIACFilter_ComPortOptionVerbsAreSurfacedNotAutoRefused proves
// DO/DONT/WILL/WONT for COM-PORT-OPTION specifically are handed back to
// the caller instead of this filter auto-refusing them the way it
// refuses every other option -- awaitComPortAgreement needs to see the
// real answer to its own WILL.
func TestIACFilter_ComPortOptionVerbsAreSurfacedNotAutoRefused(t *testing.T) {
	for _, verb := range []byte{doByte, dontByte, willByte, wontByte} {
		f := &iacFilter{}
		var reply bytes.Buffer
		_, _, verbs, err := f.feed(&reply, []byte{iacByte, verb, comPortOption})
		if err != nil {
			t.Fatalf("feed(verb=%d): %v", verb, err)
		}
		if len(verbs) != 1 || verbs[0] != verb {
			t.Errorf("verb=%d: comPortVerbs = %v, want [%d]", verb, verbs, verb)
		}
		if reply.Len() != 0 {
			t.Errorf("verb=%d: reply = %v, want none -- COM-PORT-OPTION negotiation is not auto-answered", verb, reply.Bytes())
		}
	}
}

// TestIACFilter_OtherOptionsAreStillAutoRefused proves an option OTHER
// than COM-PORT-OPTION (a console server negotiating ECHO or
// SUPPRESS-GO-AHEAD alongside it, which real servers do) still gets the
// same automatic WONT/DONT refusal pkg/telnetexec's own filter applies to
// everything.
func TestIACFilter_OtherOptionsAreStillAutoRefused(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	_, _, verbs, err := f.feed(&reply, []byte{iacByte, doByte, 1})
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(verbs) != 0 {
		t.Errorf("comPortVerbs = %v, want none for a non-COM-PORT-OPTION offer", verbs)
	}
	want := []byte{iacByte, wontByte, 1}
	if !bytes.Equal(reply.Bytes(), want) {
		t.Errorf("reply = %v, want %v", reply.Bytes(), want)
	}
}

// TestIACFilter_ParsesAComPortSubnegotiationFrame proves a full
// IAC SB 44 cmd payload IAC SE frame is parsed into its command byte and
// payload.
func TestIACFilter_ParsesAComPortSubnegotiationFrame(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	raw := []byte{iacByte, sbByte, comPortOption, cmdSetBaudRate + serverAckOffset, 0x00, 0x00, 0x25, 0x80, iacByte, seByte}
	_, frames, _, err := f.feed(&reply, raw)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %v, want exactly 1", frames)
	}
	if frames[0].cmd != cmdSetBaudRate+serverAckOffset {
		t.Errorf("cmd = %d, want %d", frames[0].cmd, cmdSetBaudRate+serverAckOffset)
	}
	want := []byte{0x00, 0x00, 0x25, 0x80}
	if !bytes.Equal(frames[0].payload, want) {
		t.Errorf("payload = %v, want %v", frames[0].payload, want)
	}
}

// TestIACFilter_ComPortFrameWithEscapedIACInPayload proves an escaped
// 0xFF byte inside a subnegotiation payload decodes to one literal byte,
// not a premature frame terminator.
func TestIACFilter_ComPortFrameWithEscapedIACInPayload(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	raw := []byte{iacByte, sbByte, comPortOption, cmdSetBaudRate, 0x00, iacByte, iacByte, 0x00, iacByte, seByte}
	_, frames, _, err := f.feed(&reply, raw)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %v, want exactly 1", frames)
	}
	want := []byte{0x00, 0xFF, 0x00}
	if !bytes.Equal(frames[0].payload, want) {
		t.Errorf("payload = %v, want %v", frames[0].payload, want)
	}
}

// TestIACFilter_FrameForAnOtherOptionIsIgnored proves a subnegotiation
// for an option other than COM-PORT-OPTION is consumed but never
// reported as a comPortFrame.
func TestIACFilter_FrameForAnOtherOptionIsIgnored(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	raw := []byte{iacByte, sbByte, 24 /* TERMINAL-TYPE */, 1, 2, 3, iacByte, seByte}
	_, frames, _, err := f.feed(&reply, raw)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(frames) != 0 {
		t.Errorf("frames = %v, want none for a non-COM-PORT-OPTION subnegotiation", frames)
	}
}

// TestIACFilter_MalformedSubnegotiationDoesNotDesynchronize proves an
// IAC byte inside SB followed by neither SE nor another IAC is absorbed
// without panicking and without misreading the stray byte as a fresh
// telnet command, recovering at the next genuine IAC SE.
func TestIACFilter_MalformedSubnegotiationDoesNotDesynchronize(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	raw := []byte{iacByte, sbByte, comPortOption, 1, iacByte, 5, iacByte, seByte, 'y'}
	data, frames, _, err := f.feed(&reply, raw)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if !bytes.Equal(data, []byte{'y'}) {
		t.Errorf("data = %v, want [y]: filter must recover to stateData after the recovered SE", data)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %v, want exactly 1 (the recovered frame)", frames)
	}
}

// TestIACFilter_StateCarriesAcrossSeparateFeedCalls proves a
// subnegotiation frame split across two separate reads (and therefore
// two separate feed calls) is still parsed correctly.
func TestIACFilter_StateCarriesAcrossSeparateFeedCalls(t *testing.T) {
	whole := []byte{iacByte, sbByte, comPortOption, cmdSetControl, 8, iacByte, seByte}

	fWhole := &iacFilter{}
	var replyWhole bytes.Buffer
	_, framesWhole, _, err := fWhole.feed(&replyWhole, whole)
	if err != nil {
		t.Fatalf("feed(whole): %v", err)
	}

	fSplit := &iacFilter{}
	var replySplit bytes.Buffer
	var framesSplit []comPortFrame
	for _, b := range whole {
		_, fr, _, err := fSplit.feed(&replySplit, []byte{b})
		if err != nil {
			t.Fatalf("feed(byte-at-a-time): %v", err)
		}
		framesSplit = append(framesSplit, fr...)
	}

	if len(framesWhole) != 1 || len(framesSplit) != 1 {
		t.Fatalf("framesWhole=%v framesSplit=%v, want exactly 1 each", framesWhole, framesSplit)
	}
	if framesWhole[0].cmd != framesSplit[0].cmd || !bytes.Equal(framesWhole[0].payload, framesSplit[0].payload) {
		t.Errorf("split-feed frame = %+v, want it identical to whole-feed frame %+v", framesSplit[0], framesWhole[0])
	}
}

// TestIACFilter_NeverPanicsOnAdversarialInput proves a handful of
// truncated and malformed sequences are absorbed without panicking.
func TestIACFilter_NeverPanicsOnAdversarialInput(t *testing.T) {
	cases := [][]byte{
		{iacByte},
		{iacByte, doByte},
		{iacByte, sbByte},
		{iacByte, sbByte, comPortOption},
		{iacByte, sbByte, comPortOption, iacByte},
		{iacByte, iacByte, iacByte},
		{iacByte, 99},
		bytes.Repeat([]byte{iacByte, sbByte, comPortOption}, 50),
	}
	for i, raw := range cases {
		f := &iacFilter{}
		var reply bytes.Buffer
		if _, _, _, err := f.feed(&reply, raw); err != nil {
			t.Errorf("case %d (%v): feed returned an error: %v", i, raw, err)
		}
	}
}

// TestSendSubneg_EscapesLiteralIACBytesInThePayload proves an outgoing
// subnegotiation argument containing a literal 0xFF byte is escaped, and
// that the resulting frame decodes back to the original bytes through
// this package's own filter -- proving the encode and decode sides agree
// with each other, not just that each looks right in isolation.
func TestSendSubneg_EscapesLiteralIACBytesInThePayload(t *testing.T) {
	var buf bytes.Buffer
	c := &Client{conn: nil}
	// sendSubneg only needs an io.Writer; conn is otherwise unused here.
	c.conn = writerOnlyConn{&buf}

	arg := []byte{0x00, 0xFF, 0x7F}
	if err := c.sendSubneg(cmdSetBaudRate, arg); err != nil {
		t.Fatalf("sendSubneg: %v", err)
	}

	f := &iacFilter{}
	var reply bytes.Buffer
	_, frames, _, err := f.feed(&reply, buf.Bytes())
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %v, want exactly 1", frames)
	}
	if frames[0].cmd != cmdSetBaudRate {
		t.Errorf("cmd = %d, want %d", frames[0].cmd, cmdSetBaudRate)
	}
	if !bytes.Equal(frames[0].payload, arg) {
		t.Errorf("payload = %v, want %v (round trip through the wire encoding)", frames[0].payload, arg)
	}
}

// TestParityToWire_CoversEveryValueExplicitly proves every
// serialline.Parity value maps to its own distinct RFC 2217 wire value,
// and an unknown value is refused rather than silently defaulted.
func TestParityToWire_CoversEveryValueExplicitly(t *testing.T) {
	cases := map[serialline.Parity]byte{
		serialline.ParityNone:  1,
		serialline.ParityOdd:   2,
		serialline.ParityEven:  3,
		serialline.ParityMark:  4,
		serialline.ParitySpace: 5,
	}
	seen := map[byte]bool{}
	for p, want := range cases {
		got, err := parityToWire(p)
		if err != nil {
			t.Fatalf("parityToWire(%v): %v", p, err)
		}
		if got != want {
			t.Errorf("parityToWire(%v) = %d, want %d", p, got, want)
		}
		if seen[got] {
			t.Errorf("wire value %d used for more than one Parity", got)
		}
		seen[got] = true
	}
	if _, err := parityToWire(serialline.Parity(99)); err == nil {
		t.Error("expected an error for an unknown Parity value")
	}
}

// TestStopBitsToWire_DoesNotRelyOnIotaOrdering proves the wire encoding
// is NOT the same ordering as serialline.StopBits' own iota: 1.5 stop
// bits is RFC 2217 wire value 3, not 1, so an arithmetic (rather than
// explicit switch) conversion would have silently swapped
// OnePointFive and Two.
func TestStopBitsToWire_DoesNotRelyOnIotaOrdering(t *testing.T) {
	cases := map[serialline.StopBits]byte{
		serialline.StopBitsOne:          1,
		serialline.StopBitsTwo:          2,
		serialline.StopBitsOnePointFive: 3,
	}
	for s, want := range cases {
		got, err := stopBitsToWire(s)
		if err != nil {
			t.Fatalf("stopBitsToWire(%v): %v", s, err)
		}
		if got != want {
			t.Errorf("stopBitsToWire(%v) = %d, want %d", s, got, want)
		}
	}
	if _, err := stopBitsToWire(serialline.StopBits(99)); err == nil {
		t.Error("expected an error for an unknown StopBits value")
	}
}

// writerOnlyConn adapts an io.Writer to a full net.Conn, for a test that
// only exercises sendSubneg's Write call and has no real connection to
// open.
type writerOnlyConn struct {
	w *bytes.Buffer
}

func (c writerOnlyConn) Read(_ []byte) (int, error)         { return 0, nil }
func (c writerOnlyConn) Write(p []byte) (int, error)        { return c.w.Write(p) }
func (c writerOnlyConn) Close() error                       { return nil }
func (c writerOnlyConn) LocalAddr() net.Addr                { return nil }
func (c writerOnlyConn) RemoteAddr() net.Addr               { return nil }
func (c writerOnlyConn) SetDeadline(_ time.Time) error      { return nil }
func (c writerOnlyConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c writerOnlyConn) SetWriteDeadline(_ time.Time) error { return nil }
