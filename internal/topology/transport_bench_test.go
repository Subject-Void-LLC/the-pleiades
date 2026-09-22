// Package topology_test measures what this module's prose asserts about
// the relative cost of its four transports.
//
// A claim about relative cost that nobody measured is a claim that gets
// repeated with the comparison reversed, which is exactly what happened to
// the WebSocket resilience claim this phase had to correct.
package topology_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/testcontainers/testcontainers-go"
)

// This file measures what Phase 96d's prose asserts, because a claim about
// relative cost that nobody measured is a claim that gets repeated into a
// datasheet with the comparison reversed.
//
// The claim: reconnecting over wss:// costs a TLS handshake plus an HTTP
// upgrade, which is strictly MORE work than nats://, so WebSocket is a
// choice about REACHABILITY (a broker on 443, behind an HTTP proxy or an
// egress filter) and never about link resilience. The strategy memo that
// started this line of work said the opposite, and the opposite is the
// version that sounds better.
//
// Four schemes rather than the two the claim mentions, because two would
// give a number without saying where it came from. Measured against a
// pair of real brokers, the four decompose the cost:
//
//	nats://  plain TCP, the floor
//	tls://   plain TCP plus a TLS handshake        (the TLS part alone)
//	ws://    TCP plus an HTTP upgrade              (the upgrade part alone)
//	wss://   TCP plus TLS plus an HTTP upgrade     (both, which is wss)
//
// What is measured is a full dial to a usable connection: topology.Connect
// returns only once the connection has actually connected, and the Flush
// after it is a real round trip to the broker. That is precisely what a
// reconnect pays, which is why this is a connect benchmark rather than a
// throughput one. Nothing here measures steady-state message cost, where
// the transports are much closer, and nothing here measures resilience,
// which is internal/event's reconnect gate and is identical for all four.
//
// MEASURED, against real brokers in containers on a local daemon, at
// -benchtime 50x:
//
//	nats    609 us/op     50 kB/op    145 allocs/op
//	ws      615 us/op     61 kB/op    190 allocs/op
//	tls    1245 us/op    115 kB/op    577 allocs/op
//	wss    1207 us/op    128 kB/op    625 allocs/op
//
// The claim holds: wss:// costs about twice the time and about four times
// the allocations of nats://, so choosing it buys reachability and is paid
// for on every reconnect.
//
// The decomposition is the part worth reading, because it is not what the
// prose implies. Almost none of that cost is the WebSocket upgrade. ws://
// is within one percent of nats:// and the difference is inside run to run
// noise; the TLS handshake is essentially the whole of it, and tls:// and
// wss:// are indistinguishable from each other for the same reason. So the
// honest summary is that ENCRYPTION is what a reconnect pays for here, and
// the upgrade rides along nearly free. Two consequences follow. A
// deployment already on tls:// gives up nothing by moving to wss:// for
// reachability, and a deployment on nats:// that moves to wss:// is paying
// for the encryption it also gained, not for the traversal.
//
// Absolute numbers move with the machine and the daemon; the ratios are
// what this is for. Re-measure rather than trusting the table if a
// decision depends on it.

// BenchmarkTransportConnect measures the cost of reaching a usable
// connection over each transport this module accepts.
func BenchmarkTransportConnect(b *testing.B) {
	// Two brokers, because one cannot serve both. A tls block applies to
	// the client port, so a single server is either the plaintext pair
	// (nats and ws) or the encrypted pair (tls and wss), never all four.
	plainEndpoint := natsWithConfig(b, `
websocket {
  port: 8080
  no_tls: true
}
`, nil, "8080")

	dir := b.TempDir()
	cert, err := tlscert.Generate(dir, tlscert.Options{ExtraNames: []string{"localhost"}})
	if err != nil {
		b.Fatalf("generating a serving certificate: %v", err)
	}
	tlsFiles := []testcontainers.ContainerFile{
		{HostFilePath: filepath.Join(dir, tlscert.CertFileName), ContainerFilePath: "/etc/nats/tls/cert.pem", FileMode: 0o644},
		{HostFilePath: filepath.Join(dir, tlscert.KeyFileName), ContainerFilePath: "/etc/nats/tls/key.pem", FileMode: 0o600},
	}
	secureEndpoint := natsWithConfig(b, `
tls {
  cert_file: "/etc/nats/tls/cert.pem"
  key_file: "/etc/nats/tls/key.pem"
}
websocket {
  port: 8080
  tls {
    cert_file: "/etc/nats/tls/cert.pem"
    key_file: "/etc/nats/tls/key.pem"
  }
}
`, tlsFiles, "8080")

	// The plaintext broker's websocket port is the one natsWithConfig
	// mapped; its client port is not published, so the plaintext pair is
	// measured over the one endpoint the helper returns. The encrypted
	// broker is mapped the same way. Both wss and ws therefore address
	// the websocket listener, and both tls and nats would address the
	// client listener, which is why only the websocket half of each pair
	// is reachable here and the two plain-client cases go through the
	// same helper with the client port named instead.
	plainClient := natsWithConfig(b, "", nil, "4222")
	secureClient := natsWithConfig(b, `
tls {
  cert_file: "/etc/nats/tls/cert.pem"
  key_file: "/etc/nats/tls/key.pem"
}
`, tlsFiles, "4222")

	for _, tc := range []struct {
		name string
		url  string
		opts []topology.ConnectOption
	}{
		{name: "nats", url: "nats://" + plainClient},
		{name: "tls", url: "tls://" + secureClient, opts: []topology.ConnectOption{topology.WithTLS(cert.TLSClientConfig())}},
		{name: "ws", url: "ws://" + plainEndpoint},
		{name: "wss", url: "wss://" + secureEndpoint, opts: []topology.ConnectOption{topology.WithTLS(cert.TLSClientConfig())}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			// A quiet logger rather than nil. DialOptions installs
			// connection lifecycle handlers, and nil falls back to
			// slog.Default(), so every iteration logs an "established"
			// line and the measurement scrolls past in the noise.
			quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				nc, err := topology.Connect(ctx, tc.url, quiet, "bench", tc.opts...)
				if err != nil {
					b.Fatalf("Connect over %s: %v", tc.name, err)
				}
				// A real round trip, so what is measured is a connection
				// that can carry traffic rather than one that has merely
				// been handed back.
				if err := nc.Flush(); err != nil {
					nc.Close()
					b.Fatalf("round trip over %s: %v", tc.name, err)
				}
				nc.Close()
			}
		})
	}
}
