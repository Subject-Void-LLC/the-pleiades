package ssh_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ssh"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
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

// TestPing_DialFailureIsReported covers the dial branch: when the TCP
// connection cannot be made, Ping must report that as a dial failure and
// must not fall through into the SSH handshake.
//
// Both targets below are unconnectable because of what the address is,
// not because of how the host's TCP stack happens to behave. That
// distinction is the whole point of this test's shape. It used to open a
// listener on 127.0.0.1:0, close it, and dial the port it had just
// released, on the assumption that a port with nothing listening refuses
// connections. That assumption is false under WSL2, and false in the
// worst possible way. Measured on such a host: loopback ports this
// process never bound refuse normally, while the port it just released
// accepts the connection, still accepts it seconds later, and only fails
// as a reset once the SSH handshake starts. The likely reason is that
// loopback is bridged with the Windows side there, so releasing the port
// on the Linux side does not release it on the other, but the reason
// matters less than the measurement: the old strategy was aiming the
// test at the one loopback address on the machine that would answer it.
// See FAILURE_PATTERNS.md #123.
func TestPing_DialFailureIsReported(t *testing.T) {
	// Ping reaches its Runner through remoteexec.Shared, which memoizes one
	// Runner per Options for the life of the PROCESS, and the breaker on it
	// counts consecutive failures with no window and no decay. Every case
	// below is a dial failure by design, so without this the counter simply
	// accumulates: one Ping spends three attempts, the threshold is five,
	// and from the third iteration of this test the error stops naming the
	// dial failure and says "circuit open" instead. That is the breaker
	// working correctly, surfacing as a failure in a test that is not about
	// it.
	t.Cleanup(remoteexec.SnapshotForTest())

	cases := []struct {
		name string
		host string
		port int
		// budget bounds how long the dial may sit there when the target
		// is silently dropped instead of actively rejected. Zero means
		// none is needed, because that case fails without waiting on the
		// network at all.
		budget time.Duration
	}{
		{
			// Port 0 is the sockets API's "assign me any free port"
			// value for bind, so no listener anywhere can ever hold it
			// and a connect to it never succeeds. It needs no route and
			// no resolver, and nothing leaves the host, which is what
			// makes it hold up inside a network-less container.
			name: "port no listener can ever hold",
			host: "127.0.0.1",
			port: 0,
		},
		{
			// TEST-NET-1 (RFC 5737) is reserved for documentation and is
			// not routed, so this is a real network-layer dial failure:
			// an immediate "network is unreachable" where there is no
			// route to it, an i/o timeout inside the budget where the
			// packets are dropped. It is here so the case above is not
			// the only evidence, since that one is settled entirely
			// inside the host.
			name:   "unroutable address",
			host:   "192.0.2.1",
			port:   22,
			budget: 1 * time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.budget > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.budget)
				defer cancel()
			}

			device := &sshStub{host: tc.host, port: tc.port}
			rc := &stubContext{secrets: map[string]string{"username": "u", "password": "p"}}
			// Without this, host key verification fails first and the
			// dial this test is about is never reached.
			params := map[string]any{"insecure_skip_host_key_verify": true}

			_, err := ssh.Ping(ctx, rc, device, params)
			if err == nil {
				t.Fatal("Ping() = nil error, want a dial failure")
			}
			if !strings.Contains(err.Error(), "dial") {
				t.Errorf("Ping() error = %q, want it to name the dial failure", err)
			}
			// Every branch after the dial also returns an error, so
			// "names dial" on its own would still pass if the connection
			// had actually been established and died later. This is the
			// assertion that pins the failure to the dial branch.
			if strings.Contains(err.Error(), "handshake") {
				t.Errorf("Ping() error = %q, want the failure reported at the dial, not at the handshake", err)
			}
		})
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
