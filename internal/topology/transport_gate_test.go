package topology_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// This file is Phase 96d's Release Gate: the transports this module claims
// to support, proven against a real nats-server configured for them.
//
// It also settles a claim that prompted the whole line of work and was
// wrong. A strategy memo asserted that WebSocket "natively tolerates" the
// link drops a satellite deployment sees. It does not: WebSocket runs over
// TCP, a reset kills it identically, and reconnecting costs a TLS
// handshake plus an HTTP Upgrade, which is strictly MORE work than plain
// nats://. What wss:// genuinely buys is reaching a broker on 443 through
// HTTP proxies, corporate egress filters and CDN termination, which
// matters a great deal for a Runner on somebody else's network and is a
// completely different property from link resilience. Nothing in this file
// asserts resilience for it; internal/event's reconnect gate owns that,
// and it owns it for every transport equally.

// natsWithConfig starts a real nats-server with an extra configuration
// file mounted, which is the mechanism Phase 96d introduced.
//
// The flags are kept and the file is added with -c rather than replacing
// them, deliberately: the deployment's flag list is pinned by a real test
// against internal/testsupport.NATSCommand, and a listener block is
// additive information rather than a different way of saying the same
// thing. Since Phase 101c that rule lives in testsupport.StartNATS, which
// is the only thing that writes a broker's command line, so this helper
// now only says which port it wants back.
//
// It returns a bare host:port with no scheme, because its callers put
// four different schemes in front of it.
func natsWithConfig(t testing.TB, conf, port string, extra ...testsupport.NATSOption) string {
	t.Helper()
	opts := append([]testsupport.NATSOption{
		testsupport.WithNATSConfig(conf),
		testsupport.WithNATSExposedPorts(port),
	}, extra...)
	return testsupport.StartNATS(t, opts...).Endpoint(port)
}

// servingCertOptions mounts a generated certificate and key where the
// configurations above name them.
//
// The files are read into memory here rather than bind mounted, which is
// the one cost of testsupport.StartNATS taking bytes. It is worth paying:
// a key that only ever exists inside a container that is thrown away
// cannot be left behind in a temp directory, and these are keys.
func servingCertOptions(t testing.TB, dir string) []testsupport.NATSOption {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s for the broker: %v", name, err)
		}
		return b
	}
	return []testsupport.NATSOption{
		testsupport.WithNATSFile("/etc/nats/tls/cert.pem", read(tlscert.CertFileName), 0o644),
		testsupport.WithNATSFile("/etc/nats/tls/key.pem", read(tlscert.KeyFileName), 0o600),
	}
}

// TestConnectOverWebSocket proves the ws:// transport works end to end
// through this module's own dial path, which is the traversal claim: a
// broker reachable on an HTTP port rather than 4222.
func TestConnectOverWebSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}

	const conf = `
websocket {
  port: 8080
  no_tls: true
}
`
	endpoint := natsWithConfig(t, conf, "8080")

	nc, err := topology.Connect(context.Background(), "ws://"+endpoint, nil, "gate")
	if err != nil {
		t.Fatalf("Connect over ws://: %v", err)
	}
	defer nc.Close()

	if !nc.IsConnected() {
		t.Fatal("ws:// connection reports not connected")
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("a round trip over ws:// failed: %v", err)
	}
}

// TestConnectOverTLS proves the tls:// transport, with a real certificate
// this repository's own tlscert package generated and a real client root
// pool, so the handshake is verified rather than skipped.
func TestConnectOverTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}

	dir := t.TempDir()
	cert, err := tlscert.Generate(dir, tlscert.Options{ExtraNames: []string{"localhost"}})
	if err != nil {
		t.Fatalf("generating a serving certificate: %v", err)
	}

	const conf = `
tls {
  cert_file: "/etc/nats/tls/cert.pem"
  key_file: "/etc/nats/tls/key.pem"
}
`
	endpoint := natsWithConfig(t, conf, "4222", servingCertOptions(t, dir)...)

	// The client trusts exactly the generated root, so a handshake that
	// succeeds proves verification rather than tolerance. TLSClientConfig
	// is consumed rather than a tls.Config being written here, which is
	// what that helper exists for: it is where InsecureSkipVerify would
	// otherwise get typed.
	nc, err := topology.Connect(context.Background(), "tls://"+endpoint, nil, "gate",
		topology.WithTLS(cert.TLSClientConfig()))
	if err != nil {
		t.Fatalf("Connect over tls://: %v", err)
	}
	defer nc.Close()

	if !nc.IsConnected() {
		t.Fatal("tls:// connection reports not connected")
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("a round trip over tls:// failed: %v", err)
	}
	if !nc.TLSRequired() && nc.ConnectedServerId() == "" {
		t.Error("the connection does not look like a TLS one")
	}
}

// TestConnectOverTLSRefusesAnUntrustedServer is the negative control for
// the test above, and the reason it is not merely asserting that a
// connection happened. Without it, a client configured to skip
// verification would pass identically.
func TestConnectOverTLSRefusesAnUntrustedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}

	serverDir := t.TempDir()
	if _, err := tlscert.Generate(serverDir, tlscert.Options{ExtraNames: []string{"localhost"}}); err != nil {
		t.Fatalf("generating the server certificate: %v", err)
	}
	// A DIFFERENT root, which never signed the server's certificate.
	otherDir := t.TempDir()
	other, err := tlscert.Generate(otherDir, tlscert.Options{ExtraNames: []string{"localhost"}})
	if err != nil {
		t.Fatalf("generating an unrelated root: %v", err)
	}

	const conf = `
tls {
  cert_file: "/etc/nats/tls/cert.pem"
  key_file: "/etc/nats/tls/key.pem"
}
`
	endpoint := natsWithConfig(t, conf, "4222", servingCertOptions(t, serverDir)...)

	nc, err := topology.Connect(context.Background(), "tls://"+endpoint, nil, "gate",
		topology.WithTLS(other.TLSClientConfig()))
	if err == nil {
		nc.Close()
		t.Fatal("Connect trusted a server whose certificate its root pool never signed")
	}
}
