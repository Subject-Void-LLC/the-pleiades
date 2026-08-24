package topology_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
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
// thing.
func natsWithConfig(t *testing.T, conf string, extraFiles []testcontainers.ContainerFile, port string) string {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "nats.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatalf("writing the broker config: %v", err)
	}

	files := append([]testcontainers.ContainerFile{{
		HostFilePath:      confPath,
		ContainerFilePath: "/etc/nats/nats.conf",
		FileMode:          0o644,
	}}, extraFiles...)

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        testsupport.NATSImage,
			Cmd:          append([]string{"-c", "/etc/nats/nats.conf"}, testsupport.NATSCommand()...),
			Files:        files,
			ExposedPorts: []string{port + "/tcp"},
			WaitingFor:   wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout),
		},
		Started: true,
	}
	c, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("starting nats with a config file: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(c) })

	mapped, err := c.MappedPort(ctx, port+"/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	return host + ":" + mapped.Port()
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
	endpoint := natsWithConfig(t, conf, nil, "8080")

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
	files := []testcontainers.ContainerFile{
		{HostFilePath: filepath.Join(dir, tlscert.CertFileName), ContainerFilePath: "/etc/nats/tls/cert.pem", FileMode: 0o644},
		{HostFilePath: filepath.Join(dir, tlscert.KeyFileName), ContainerFilePath: "/etc/nats/tls/key.pem", FileMode: 0o600},
	}
	endpoint := natsWithConfig(t, conf, files, "4222")

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
	files := []testcontainers.ContainerFile{
		{HostFilePath: filepath.Join(serverDir, tlscert.CertFileName), ContainerFilePath: "/etc/nats/tls/cert.pem", FileMode: 0o644},
		{HostFilePath: filepath.Join(serverDir, tlscert.KeyFileName), ContainerFilePath: "/etc/nats/tls/key.pem", FileMode: 0o600},
	}
	endpoint := natsWithConfig(t, conf, files, "4222")

	nc, err := topology.Connect(context.Background(), "tls://"+endpoint, nil, "gate",
		topology.WithTLS(other.TLSClientConfig()))
	if err == nil {
		nc.Close()
		t.Fatal("Connect trusted a server whose certificate its root pool never signed")
	}
}
