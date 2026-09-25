// Tests for pleiades.builtin.connection.reset, against a real SSH server
// whose login count shows whether the connection was really closed.
package connection_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pleiades/builtin/connection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestReset_Registered proves the manifest says what the engine relies
// on: implemented, checkable by running, and ending the login session.
func TestReset_Registered(t *testing.T) {
	d, ok := collection.Lookup("pleiades.builtin.connection.reset")
	if !ok {
		t.Fatal("pleiades.builtin.connection.reset is not registered")
	}
	if d.Manifest.Status != collection.StatusImplemented || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Errorf("manifest = %+v, want implemented and checkable", d.Manifest)
	}
	if !d.Manifest.EndsLoginSession {
		t.Error("EndsLoginSession = false; the Walk tier relies on it to close the connection")
	}
}

// pooledContext is a RunbookContext lending from a pool, as the engine's
// own is when a device's connections persist.
type pooledContext struct {
	secrets map[string]string
	pool    *remoteexec.Pool
}

func (c pooledContext) InjectSecrets() map[string]string { return c.secrets }
func (pooledContext) SetStat(string, interface{}) error  { return nil }
func (pooledContext) EmitFact(string, interface{}) error { return nil }
func (c pooledContext) ConnectionPool() *remoteexec.Pool { return c.pool }

// sshDevice is the shared inventory stub plus the accessors
// capability.SSHTransportCapable requires.
type sshDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *sshDevice) SSHHost() string { return d.host }
func (d *sshDevice) SSHPort() int    { return d.port }

// newDevice is an SSH-reachable device at srv's address.
func newDevice(t *testing.T, srv *remoteexectest.Server) *sshDevice {
	t.Helper()
	return &sshDevice{
		Stub: &inventorytest.Stub{
			StubID:    "reset-1",
			StubName:  "web1",
			Caps:      []capability.Name{capability.NameSSHTransport},
			StubState: inventory.StateActive,
		},
		host: srv.Host,
		port: srv.Port,
	}
}

// TestReset_NextTaskLogsInAgain runs a task, resets, and runs another,
// through sdk.Connect as a real method does, and counts the server's
// logins: two with the reset, where two tasks without it share one.
func TestReset_NextTaskLogsInAgain(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	device := newDevice(t, srv)
	pool := remoteexec.NewPool(0)
	t.Cleanup(func() { _ = pool.Close() })
	rc := pooledContext{secrets: map[string]string{wire.SecretUsername: srv.Username, wire.SecretPassword: srv.Password}, pool: pool}
	params := map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}

	run := func() {
		t.Helper()
		conn, err := sdk.Connect(context.Background(), rc, device, params, "test")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Run(context.Background(), "true"); err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
	run()
	run()
	if got := srv.Logins(); got != 1 {
		t.Fatalf("two tasks without a reset: %d logins, want 1", got)
	}
	res, err := connection.Reset(context.Background(), rc, device, nil)
	if err != nil || res.Changed {
		t.Fatalf("Reset = %+v, %v; want no change and no error", res, err)
	}
	run()
	if got := srv.Logins(); got != 2 {
		t.Fatalf("after a reset: %d logins, want 2", got)
	}
}

// TestReset_WithoutAPoolDoesNothing covers a run whose connections do not
// persist, and a context that is not the engine's.
func TestReset_WithoutAPoolDoesNothing(t *testing.T) {
	res, err := connection.Reset(context.Background(), pooledContext{}, nil, nil)
	if err != nil || res.Changed {
		t.Fatalf("Reset with no pool = %+v, %v; want no change and no error", res, err)
	}
}
