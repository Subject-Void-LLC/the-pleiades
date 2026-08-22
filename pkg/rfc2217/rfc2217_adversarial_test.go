package rfc2217_test

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/rfc2217"
)

// This file is Phase 73's own adversarial proof for pkg/rfc2217's
// central defense: a compromised or malfunctioning access server that
// sends unsolicited COM-PORT-OPTION subnegotiations — a server-initiated
// baud change, break, or modem-control-line assertion the operator never
// asked for — must never be applied. Client holds no local cache of
// "the line's current settings" an inbound frame could corrupt (see the
// package doc comment), so the only way to prove this is behavioral: an
// unsolicited frame must never leak into Read() as console data, must
// never crash or wedge the client, and must never interfere with a
// genuinely requested operation that follows it.

// TestClient_UnsolicitedBreakInjectionIsIgnoredNotApplied proves a
// server that sends SERVER_SET_CONTROL(break-on) — the exact "server-
// initiated break" the plan names — with no request ever made for it is
// silently dropped: the client neither errors nor treats it as
// meaningful, and console data sent immediately afterward still reaches
// Read() intact.
func TestClient_UnsolicitedBreakInjectionIsIgnoredNotApplied(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		writeAck(t, conn, cmdSetControl+serverAckOff, []byte{5}) // unsolicited: break-on, never requested
		_, _ = conn.Write([]byte("console output after injection\r\n"))
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got []byte
	buf := make([]byte, 256)
	deadline := time.Now().Add(3 * time.Second)
	want := "console output after injection\r\n"
	for len(got) < len(want) && time.Now().Before(deadline) {
		n, err := c.Read(ctx, buf)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		got = append(got, buf[:n]...)
	}
	if string(got) != want {
		t.Errorf("Read accumulated %q, want exactly %q — the injected break frame must not leak into it or corrupt what follows", got, want)
	}
}

// TestClient_UnsolicitedBaudChangeInjectionDoesNotAffectALaterLegitimateRequest
// proves a server that injects an unsolicited SERVER_SET_BAUDRATE (a
// value the client never asked for, at a moment it never expected one)
// does not corrupt the client's own request/response tracking: a
// genuinely requested AssertRTS sent immediately afterward still
// receives its OWN correct acknowledgement, not the injected frame
// mistaken for it, and not a hang waiting for something that will never
// arrive because the injected frame was consumed by the wrong waiter.
func TestClient_UnsolicitedBaudChangeInjectionDoesNotAffectALaterLegitimateRequest(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		// Unsolicited, hostile-looking baud value, sent before the client
		// has asked for anything.
		writeAck(t, conn, cmdSetBaudRate+serverAckOff, []byte{0xFF, 0xFF, 0xFF, 0xFF})

		// The client's own, real, later request: RTS-on.
		cmd, payload := readSubnegFrame(t, conn)
		if cmd != cmdSetControl || !bytes.Equal(payload, []byte{11}) {
			t.Errorf("RTS-on frame = cmd %d payload %v, want cmd %d payload [11]", cmd, payload, cmdSetControl)
		}
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.AssertRTS(ctx, true); err != nil {
		t.Fatalf("AssertRTS after an unsolicited injected frame: %v", err)
	}
}

// TestClient_RepeatedUnsolicitedInjectionsNeverPanic proves a barrage of
// different unsolicited SET_CONTROL sub-commands (break, DTR, RTS, in
// both directions) arriving back to back, with no request ever made for
// any of them, is absorbed without a panic and without wedging the
// client — the same "fail closed, never crash" bar this phase's own
// fuzz targets hold every other attacker-reachable parser to.
// TestClient_UnterminatedSubnegotiationPayloadFailsClosedRatherThanGrowingWithoutBound
// proves a COM-PORT-OPTION subnegotiation that never sends its own
// terminating IAC SE -- a compromised or malfunctioning access server
// holding Client's payload accumulator open for as long as one
// Options.ReadTimeout window allows -- surfaces as a genuine error
// instead of growing without bound for that whole window. Found during
// Phase 73's own Schema/Injection Hardening audit: RFC 2217
// subnegotiation carries no length field this package could trust or
// distrust (framing is IAC-SE-terminated, not length-prefixed), but an
// attacker withholding that terminator is the same class of unbounded-
// accumulation risk a length field lying about its own size would be.
func TestClient_UnterminatedSubnegotiationPayloadFailsClosedRatherThanGrowingWithoutBound(t *testing.T) {
	// Gated behind negotiated, signaled only once Dial has already
	// returned client-side, rather than built on dialAgreeing: the
	// hostile payload must arrive strictly AFTER negotiation completes,
	// not merely after the server has written its DO reply, since the
	// two could otherwise land in the same client-side read and this
	// test would (correctly, but not on purpose) be proving the bound
	// applies during negotiation instead of during ordinary Read.
	negotiated := make(chan struct{})
	port := fakeAccessServer(t, func(conn net.Conn) {
		defer conn.Close()
		negotiateComPortOption(t, conn, doByte)
		<-negotiated
		_, _ = conn.Write([]byte{iacByte, sbByte, comPortOption})
		_, _ = conn.Write(bytes.Repeat([]byte{0x41}, 4096)) // never terminated with IAC SE
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: time.Second})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	close(negotiated)

	buf := make([]byte, 256)
	if _, err := c.Read(ctx, buf); err == nil {
		t.Fatal("expected an error once the unterminated subnegotiation payload exceeded its own bound")
	}
}

func TestClient_RepeatedUnsolicitedInjectionsNeverPanic(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		for _, value := range []byte{5, 6, 8, 9, 11, 12} {
			writeAck(t, conn, cmdSetControl+serverAckOff, []byte{value})
		}
		_, _ = conn.Write([]byte("still-alive\r\n"))
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got []byte
	buf := make([]byte, 256)
	deadline := time.Now().Add(3 * time.Second)
	want := "still-alive\r\n"
	for len(got) < len(want) && time.Now().Before(deadline) {
		n, err := c.Read(ctx, buf)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		got = append(got, buf[:n]...)
	}
	if string(got) != want {
		t.Errorf("Read accumulated %q, want exactly %q", got, want)
	}
}
