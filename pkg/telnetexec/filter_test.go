package telnetexec

import (
	"bytes"
	"testing"
)

// TestIACFilter_PlainDataPassesThroughUnchanged proves the common case:
// bytes carrying no IAC byte at all are returned verbatim, with no reply
// written.
func TestIACFilter_PlainDataPassesThroughUnchanged(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	data, err := f.feed(&reply, []byte("hello world\r\n"))
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if string(data) != "hello world\r\n" {
		t.Errorf("data = %q, want %q", data, "hello world\r\n")
	}
	if reply.Len() != 0 {
		t.Errorf("reply = %v, want none", reply.Bytes())
	}
}

// TestIACFilter_EscapedIACByteBecomesLiteral0xFF proves IAC IAC decodes
// to one literal 0xFF data byte, not two, and not a negotiation attempt.
func TestIACFilter_EscapedIACByteBecomesLiteral0xFF(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	data, err := f.feed(&reply, []byte{'a', iacByte, iacByte, 'b'})
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	want := []byte{'a', 0xFF, 'b'}
	if !bytes.Equal(data, want) {
		t.Errorf("data = %v, want %v", data, want)
	}
	if reply.Len() != 0 {
		t.Errorf("reply = %v, want none", reply.Bytes())
	}
}

// TestIACFilter_RefusesDOWithWONT proves a DO offer for any option is
// answered with WONT, and contributes no bytes to the plain data output.
func TestIACFilter_RefusesDOWithWONT(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	data, err := f.feed(&reply, []byte{iacByte, doByte, 42})
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("data = %v, want none", data)
	}
	want := []byte{iacByte, wontByte, 42}
	if !bytes.Equal(reply.Bytes(), want) {
		t.Errorf("reply = %v, want %v", reply.Bytes(), want)
	}
}

// TestIACFilter_RefusesWILLWithDONT proves a WILL offer for any option is
// answered with DONT.
func TestIACFilter_RefusesWILLWithDONT(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	data, err := f.feed(&reply, []byte{iacByte, willByte, 7})
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("data = %v, want none", data)
	}
	want := []byte{iacByte, dontByte, 7}
	if !bytes.Equal(reply.Bytes(), want) {
		t.Errorf("reply = %v, want %v", reply.Bytes(), want)
	}
}

// TestIACFilter_DONTAndWONTNeedNoReply proves DONT/WONT (confirmations
// of a refusal this client already holds) provoke no reply at all,
// avoiding a negotiation loop with a server that follows the same rule.
func TestIACFilter_DONTAndWONTNeedNoReply(t *testing.T) {
	for _, verb := range []byte{dontByte, wontByte} {
		f := &iacFilter{}
		var reply bytes.Buffer
		data, err := f.feed(&reply, []byte{iacByte, verb, 1})
		if err != nil {
			t.Fatalf("feed(verb=%d): %v", verb, err)
		}
		if len(data) != 0 {
			t.Errorf("verb=%d: data = %v, want none", verb, data)
		}
		if reply.Len() != 0 {
			t.Errorf("verb=%d: reply = %v, want none", verb, reply.Bytes())
		}
	}
}

// TestIACFilter_SubnegotiationPayloadIsDroppedNotLeaked proves an
// IAC SB ... IAC SE frame contributes nothing to the plain data output
// and provokes no reply, since this client never WILLs or DOs any
// option, so it should never legitimately receive one and must not act
// on it if a server sends one anyway.
func TestIACFilter_SubnegotiationPayloadIsDroppedNotLeaked(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	raw := []byte{iacByte, sbByte, 44, 1, 2, 3, iacByte, seByte}
	data, err := f.feed(&reply, raw)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("data = %v, want none", data)
	}
	if reply.Len() != 0 {
		t.Errorf("reply = %v, want none", reply.Bytes())
	}
}

// TestIACFilter_SubnegotiationWithEscapedIACInPayload proves an IAC IAC
// escape sequence INSIDE a subnegotiation payload is consumed as the
// escape it is, not misread as the frame's closing IAC SE.
func TestIACFilter_SubnegotiationWithEscapedIACInPayload(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	// IAC SB 44 0xFF(escaped) IAC SE, followed by plain data that must
	// still be parsed correctly once the state machine returns to
	// stateData.
	raw := []byte{iacByte, sbByte, 44, iacByte, iacByte, iacByte, seByte, 'x'}
	data, err := f.feed(&reply, raw)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if !bytes.Equal(data, []byte{'x'}) {
		t.Errorf("data = %v, want [x] (state must resync to stateData after SE)", data)
	}
}

// TestIACFilter_MalformedSubnegotiationDoesNotDesynchronize proves an
// IAC byte inside SB followed by neither SE nor another IAC (a truncated
// or hostile frame) is absorbed without panicking and without
// misreading the stray byte as an arbitrary fresh telnet command.
func TestIACFilter_MalformedSubnegotiationDoesNotDesynchronize(t *testing.T) {
	f := &iacFilter{}
	var reply bytes.Buffer
	// IAC SB 44 IAC <garbage, neither SE nor IAC> ... IAC SE 'y'
	raw := []byte{iacByte, sbByte, 44, iacByte, 5, iacByte, seByte, 'y'}
	data, err := f.feed(&reply, raw)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if !bytes.Equal(data, []byte{'y'}) {
		t.Errorf("data = %v, want [y]: filter must recover to stateData at the next real IAC SE", data)
	}
}

// TestIACFilter_StateCarriesAcrossSeparateFeedCalls proves a telnet
// command split across two separate reads (and therefore two separate
// feed calls) is still parsed correctly -- the real reason filterState
// is a field on iacFilter rather than a local variable inside feed.
func TestIACFilter_StateCarriesAcrossSeparateFeedCalls(t *testing.T) {
	whole := []byte{iacByte, doByte, 9}

	fWhole := &iacFilter{}
	var replyWhole bytes.Buffer
	if _, err := fWhole.feed(&replyWhole, whole); err != nil {
		t.Fatalf("feed(whole): %v", err)
	}

	fSplit := &iacFilter{}
	var replySplit bytes.Buffer
	for _, b := range whole {
		if _, err := fSplit.feed(&replySplit, []byte{b}); err != nil {
			t.Fatalf("feed(byte-at-a-time): %v", err)
		}
	}

	if !bytes.Equal(replyWhole.Bytes(), replySplit.Bytes()) {
		t.Errorf("split-feed reply = %v, want it identical to whole-feed reply %v", replySplit.Bytes(), replyWhole.Bytes())
	}
}

// TestIACFilter_NeverPanicsOnAdversarialInput proves a handful of
// truncated and malformed sequences -- exactly the shapes a fuzzer would
// find first -- are absorbed without panicking, each producing some
// (possibly empty) data slice and no crash. This is a fixed adversarial
// set, not a corpus-based fuzz target: Phase 73's own plan scopes
// corpus-based Telnet framing fuzzing to a later workstream's fuzz list,
// which does not name bare Telnet IAC framing specifically, but "never
// panics" is cheap enough to prove here directly.
func TestIACFilter_NeverPanicsOnAdversarialInput(t *testing.T) {
	cases := [][]byte{
		{iacByte},                      // truncated: IAC with nothing after it
		{iacByte, doByte},              // truncated: DO with no option byte
		{iacByte, sbByte},              // truncated: SB with nothing after it
		{iacByte, sbByte, 44},          // truncated: SB option with no payload or SE
		{iacByte, sbByte, 44, iacByte}, // truncated: SB payload ending mid-IAC
		{iacByte, iacByte, iacByte},    // odd number of IAC bytes
		{iacByte, 99},                  // unknown single-byte command after IAC
		bytes.Repeat([]byte{iacByte, sbByte, 44}, 50), // repeated SB opens with no closes
	}
	for i, raw := range cases {
		f := &iacFilter{}
		var reply bytes.Buffer
		if _, err := f.feed(&reply, raw); err != nil {
			t.Errorf("case %d (%v): feed returned an error: %v", i, raw, err)
		}
	}
}

// TestWriteData_EscapesLiteralIACBytes proves an outgoing literal 0xFF
// data byte is escaped as IAC IAC before being written, so the far end
// (running the identical filter this package implements) decodes it back
// to one literal byte rather than misreading it as the start of a telnet
// command.
func TestWriteData_EscapesLiteralIACBytes(t *testing.T) {
	var buf bytes.Buffer
	if err := writeData(&buf, []byte{'a', 0xFF, 'b'}); err != nil {
		t.Fatalf("writeData: %v", err)
	}
	want := []byte{'a', iacByte, iacByte, 'b'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("written = %v, want %v", buf.Bytes(), want)
	}
}

// TestWriteData_NoIACBytesIsUnchanged proves the common case is a
// pass-through with no escaping overhead.
func TestWriteData_NoIACBytesIsUnchanged(t *testing.T) {
	var buf bytes.Buffer
	if err := writeData(&buf, []byte("show version\r\n")); err != nil {
		t.Fatalf("writeData: %v", err)
	}
	if buf.String() != "show version\r\n" {
		t.Errorf("written = %q, want %q", buf.String(), "show version\r\n")
	}
}
