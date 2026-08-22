package rfc2217_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/rfc2217"
)

// TestDial_DroppedConnectionMidNegotiationSurfacesARealErrorAndNoClient
// is this phase's own Chaos Testing proof for RFC 2217: a compromised or
// simply failing access server that reads the client's own
// WILL COM-PORT-OPTION and then drops the connection abruptly (a real
// TCP RST, forced deterministically via SO_LINGER=0 the same way
// pkg/telnetexec's own genuine-read-error test forces one, not a
// timing-dependent trick) — never sending DO, DONT, or anything else.
//
// Two things must both be true, and this test checks both: Dial returns
// a genuine, non-nil error (never hangs, never silently returns a
// half-negotiated Client), and Dial returns a nil *Client. The second
// assertion is the strongest possible form of "no negotiated state held
// across it": there is no Client value at all for a caller to
// accidentally use after a failed negotiation, so there is nothing a
// caller could misuse as if it had actually completed.
func TestDial_DroppedConnectionMidNegotiationSurfacesARealErrorAndNoClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			conn.Close()
			return
		}
		// Read the client's own IAC WILL COM-PORT-OPTION (3 bytes), so
		// the drop genuinely lands mid-negotiation rather than before
		// the client has sent anything at all.
		buf := make([]byte, 3)
		_, _ = tcpConn.Read(buf)
		// SO_LINGER=0 makes the next Close() send a real RST instead of
		// a clean FIN, so the client's next Read fails with "connection
		// reset by peer" -- a genuine error, not a timeout and not a
		// clean EOF.
		_ = tcpConn.SetLinger(0)
		_ = tcpConn.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	client, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: 3 * time.Second})
	elapsed := time.Since(start)

	if err == nil {
		if client != nil {
			_ = client.Close()
		}
		t.Fatal("expected a real error when the connection is dropped mid-negotiation")
	}
	if client != nil {
		t.Errorf("expected a nil *Client on a failed Dial, got %v -- a non-nil Client here would be exactly the \"negotiated state held across a failure\" this test exists to rule out", client)
	}
	if elapsed > 4*time.Second {
		t.Errorf("Dial took %v to fail, want it to fail promptly on the real connection reset rather than waiting out ReadTimeout", elapsed)
	}
	t.Logf("dropped mid-negotiation failed as expected after %v: %v", elapsed, err)
}

// TestDial_ConnectionClosedAfterAgreementButBeforeUseIsNotReusable is the
// complementary case: negotiation completes normally (the access server
// genuinely agrees), but the connection is then severed before the
// caller does anything else with it. The returned Client must not
// pretend the session is still usable — a subsequent operation must
// fail with a real error, never hang or silently no-op.
func TestDial_ConnectionClosedAfterAgreementButBeforeUseIsNotReusable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 3)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte{255, 253, 44}) // IAC DO COM-PORT-OPTION: genuine agreement
		tcpConn, ok := conn.(*net.TCPConn)
		if ok {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	if err := client.AssertDTR(ctx, true); err == nil {
		t.Fatal("expected AssertDTR to fail against a connection the server already closed")
	}
}
