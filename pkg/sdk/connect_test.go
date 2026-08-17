package sdk_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// sshDevice is an inventory item reachable over SSH: the shared stub plus
// the two accessors capability.SSHTransportCapable requires, which that
// stub deliberately does not provide.
type sshDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *sshDevice) SSHHost() string { return d.host }
func (d *sshDevice) SSHPort() int    { return d.port }

func newStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "sdk-1",
		StubName:  "sdk-target",
		Caps:      []capability.Name{capability.NameSSHTransport},
		StubState: inventory.StateActive,
	}
}

// startServer brings up the real in-process SSH server and stops it when
// the test ends.
func startServer(t *testing.T) *remoteexectest.Server {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// secretContext is a RunbookContext carrying a fixed secret set, standing
// in for the real one a composition root builds from the credential store
// or the dispatch payload.
type secretContext struct {
	*recordingContext
	secrets map[string]string
}

func (c *secretContext) InjectSecrets() map[string]string { return c.secrets }

func newSecretContext(secrets map[string]string) *secretContext {
	return &secretContext{recordingContext: newRecordingContext(), secrets: secrets}
}

// TestConnect_ReachesARealServer is the success path, against a real SSH
// server doing a real key exchange and a real authentication round.
//
// It also runs a command through the returned connection, because a
// Connect that handshook and returned something unusable would pass a
// test that only checked for a nil error.
func TestConnect_ReachesARealServer(t *testing.T) {
	srv := startServer(t)
	device := &sshDevice{Stub: newStub(), host: srv.Host, port: srv.Port}
	rc := newSecretContext(srv.Secrets())

	conn, err := sdk.Connect(context.Background(), rc, device, map[string]any{
		sdk.ParamInsecureSkipHostKeyVerify: true,
	}, "test.connect")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	result, err := conn.Run(context.Background(), "echo reachable")
	if err != nil {
		t.Fatalf("running through the returned connection: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "reachable" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "reachable")
	}
}

// TestConnect_VerifiesTheHostKeyByDefault proves the fail-closed default
// survives this wrapper: with no opt-out and a real known_hosts holding
// the server's real key, the connection succeeds, and with an empty
// known_hosts it is refused.
//
// Both halves are needed. The success alone would pass against a wrapper
// that skipped verification entirely.
func TestConnect_VerifiesTheHostKeyByDefault(t *testing.T) {
	srv := startServer(t)
	device := &sshDevice{Stub: newStub(), host: srv.Host, port: srv.Port}
	rc := newSecretContext(srv.Secrets())

	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{srv.Addr()}, srv.HostKey)
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)

	conn, err := sdk.Connect(context.Background(), rc, device, nil, "test.connect")
	if err != nil {
		t.Fatalf("Connect with a real known_hosts entry: %v", err)
	}
	_ = conn.Close()

	// The same device, verified against a file that knows nothing about
	// it, must be refused rather than trusted.
	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatalf("writing an empty known_hosts: %v", err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, empty)

	if _, err := sdk.Connect(context.Background(), rc, device, nil, "test.connect"); err == nil {
		t.Fatal("a host with no known_hosts entry was trusted")
	}
}

func TestConnect_Refusals(t *testing.T) {
	srv := startServer(t)

	tests := []struct {
		name    string
		device  inventory.InventoryItem
		secrets map[string]string
		want    string
	}{
		{
			name:    "no device at all",
			device:  nil,
			secrets: srv.Secrets(),
			want:    "no target device",
		},
		{
			// The bare stub implements InventoryItem and neither SSH
			// accessor, so it cannot be reached at all.
			name:    "a device with no SSH transport",
			device:  newStub(),
			secrets: srv.Secrets(),
			want:    "not reachable over SSH",
		},
		{
			// Refused before any dial. Proceeding would let a device with
			// no stored credential attempt an unauthenticated login, which
			// is never what the caller meant.
			name:    "a device with no credential",
			device:  &sshDevice{Stub: newStub(), host: srv.Host, port: srv.Port},
			secrets: map[string]string{},
			want:    "no usable authentication",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rc := newSecretContext(tc.secrets)
			_, err := sdk.Connect(context.Background(), rc, tc.device, map[string]any{
				sdk.ParamInsecureSkipHostKeyVerify: true,
			}, "test.connect")
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			// Every refusal names the method, because an operator reading a
			// failed run needs to know which task produced it.
			if !strings.Contains(err.Error(), "test.connect") {
				t.Errorf("error = %v, want it to name the calling method", err)
			}
		})
	}
}
