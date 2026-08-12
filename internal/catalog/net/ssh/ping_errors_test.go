package ssh_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ssh"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// sshStub is an inventory item that implements capability.SSHTransportCapable,
// which pkg/inventory/inventorytest.Stub deliberately does not: these tests
// need Ping to get past its transport-capability guard so the branches
// after it become reachable.
type sshStub struct {
	host string
	port int
}

func (s *sshStub) ID() inventory.DeviceID           { return "stub-1" }
func (s *sshStub) Name() string                     { return "stub" }
func (s *sshStub) Properties() inventory.Properties { return inventory.NewProperties(nil) }
func (s *sshStub) Tags() []inventory.Tag            { return nil }
func (s *sshStub) HasCapability(name capability.Name) bool {
	return name == capability.NameSSHTransport
}
func (s *sshStub) Capabilities() []capability.Name {
	return []capability.Name{capability.NameSSHTransport}
}
func (s *sshStub) AddInfo(string, inventory.PropertyValue, bool) error { return nil }
func (s *sshStub) RemoveInfo(string) error                             { return nil }
func (s *sshStub) ShowInfo() inventory.Properties                      { return inventory.NewProperties(nil) }
func (s *sshStub) Version() uint64                                     { return 0 }
func (s *sshStub) History() []inventory.Revision                       { return nil }
func (s *sshStub) State() inventory.LifecycleState                     { return inventory.StateActive }
func (s *sshStub) Source() inventory.SourceAuthority                   { return inventory.SourceAuthority{} }
func (s *sshStub) SSHHost() string                                     { return s.host }
func (s *sshStub) SSHPort() int                                        { return s.port }

// stubContext is a minimal sdk.RunbookContext carrying a fixed secret set,
// standing in for the real one the IPC child builds from the dispatch
// payload's JIT-delivered credential.
type stubContext struct {
	secrets map[string]string
	stats   map[string]interface{}

	// statErr, when set, makes SetStat fail, so a test can drive the branch
	// where the remote command succeeded and recording its answer did not.
	statErr error
}

func (c *stubContext) InjectSecrets() map[string]string { return c.secrets }
func (c *stubContext) SetStat(key string, value interface{}) error {
	if c.statErr != nil {
		return c.statErr
	}
	if c.stats == nil {
		c.stats = map[string]interface{}{}
	}
	c.stats[key] = value
	return nil
}
func (c *stubContext) EmitFact(key string, value interface{}) error { return c.SetStat(key, value) }

// TestPing_NoUsableSecretRefusesBeforeDialing proves Ping fails on a
// device with no credential rather than opening a connection and
// attempting an unauthenticated login. The address it is given is
// deliberately unroutable, so a test that started dialing anyway would
// fail with a dial error instead of the auth error asserted here.
func TestPing_NoUsableSecretRefusesBeforeDialing(t *testing.T) {
	device := &sshStub{host: "192.0.2.1", port: 22} // TEST-NET-1, RFC 5737
	rc := &stubContext{secrets: map[string]string{}}

	_, err := ssh.Ping(context.Background(), rc, device, nil)
	if err == nil {
		t.Fatal("Ping() = nil error, want a refusal when no credential is available")
	}
	if strings.Contains(err.Error(), "dial") {
		t.Errorf("Ping() error = %q, want the auth refusal to happen before any dial", err)
	}
}

// TestPing_MissingKnownHostsFailsClosed proves host key verification is
// fail-closed at the Collection level too: with no known_hosts file and no
// explicit opt-out, Ping refuses rather than trusting whatever key the
// remote presents.
func TestPing_MissingKnownHostsFailsClosed(t *testing.T) {
	// hostKeyCallback resolves $HOME/.ssh/known_hosts and takes no path
	// parameter, so pointing HOME at an empty directory is what actually
	// reproduces "this operator has no known_hosts yet."
	t.Setenv("HOME", t.TempDir())

	device := &sshStub{host: "192.0.2.1", port: 22}
	rc := &stubContext{secrets: map[string]string{"username": "u", "password": "p"}}

	_, err := ssh.Ping(context.Background(), rc, device, nil)
	if err == nil {
		t.Fatal("Ping() = nil error, want a refusal when known_hosts is missing")
	}
	if strings.Contains(err.Error(), "dial") {
		t.Errorf("Ping() error = %q, want host key verification to fail before any dial", err)
	}
}

// TestPing_DialFailureIsReported covers the dial branch against a port
// that is genuinely closed: a listener is opened to claim a free port and
// closed immediately, so nothing can be listening on it when Ping tries.
func TestPing_DialFailureIsReported(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening a listener to claim a port: %v", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is %T, want *net.TCPAddr", listener.Addr())
	}
	port := addr.Port
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}

	device := &sshStub{host: "127.0.0.1", port: port}
	rc := &stubContext{secrets: map[string]string{"username": "u", "password": "p"}}
	params := map[string]any{"insecure_skip_host_key_verify": true}

	_, err = ssh.Ping(context.Background(), rc, device, params)
	if err == nil {
		t.Fatal("Ping() = nil error, want a dial failure against a closed port")
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Errorf("Ping() error = %q, want it to name the dial failure", err)
	}
}

// TestPing_CanceledContextDoesNotHang proves Ping honors ctx cancellation
// at the dial, which is what makes an interruptible job's self-abort
// (internal/runner's executeWithLease) actually reach this method rather
// than waiting out the full dial timeout.
func TestPing_CanceledContextDoesNotHang(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	device := &sshStub{host: "192.0.2.1", port: 22} // unroutable: would otherwise hang until timeout
	rc := &stubContext{secrets: map[string]string{"username": "u", "password": "p"}}
	params := map[string]any{"insecure_skip_host_key_verify": true}

	done := make(chan error, 1)
	go func() {
		_, err := ssh.Ping(ctx, rc, device, params)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Ping() = nil error, want a failure on an already-canceled context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ping() did not return promptly on a canceled context; ctx is not reaching the dial")
	}
}
