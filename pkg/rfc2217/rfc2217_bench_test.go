package rfc2217_test

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/rfc2217"
)

// BenchmarkDial_Negotiation measures the real per-call cost of RFC 2217's
// own negotiation handshake (Dial's WILL COM-PORT-OPTION / await DO
// exchange) against a real, independent-per-connection fake access
// server on loopback TCP. This is what a runbook actually pays: unlike
// SSH (one handshake amortized across a whole session), RFC 2217 has no
// session concept here at all, so a per-command caller renegotiates
// every single time — this benchmark exists to make that real,
// measurable cost visible, not to measure ser2net's own performance (a
// real daemon would add real I/O latency this in-process fake server
// deliberately has none of, keeping the number specific to this
// package's own negotiation logic).
func BenchmarkDial_Negotiation(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 3)
				if _, err := conn.Read(buf); err != nil {
					return
				}
				_, _ = conn.Write([]byte{255, 253, 44}) // IAC DO COM-PORT-OPTION
				// Blocks here until the client closes its side (Close,
				// at the end of each benchmark iteration below), rather
				// than on a channel that would never close: that would
				// leak one goroutine per iteration for the life of the
				// process instead of exiting cleanly with the
				// connection.
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		b.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		b.Fatalf("parse port: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		client, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: 2 * time.Second})
		cancel()
		if err != nil {
			b.Fatalf("Dial: %v", err)
		}
		_ = client.Close()
	}
}
