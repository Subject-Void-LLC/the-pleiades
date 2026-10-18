// The harness build.lock.verify's tests run against: a real SSH server in
// this process running each command through a real /bin/sh on this
// machine, so the files it reads are real.
package lock_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// device is an SSH device that runs commands.
type device struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *device) SSHHost() string          { return d.host }
func (d *device) SSHPort() int             { return d.port }
func (d *device) WorkingDirectory() string { return "" }

// stubContext keeps every return a method sets.
type stubContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *stubContext) InjectSecrets() map[string]string     { return c.secrets }
func (c *stubContext) SetStat(key string, value any) error  { c.stats[key] = value; return nil }
func (c *stubContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// harness is one test's device, context and project directory.
type harness struct {
	device  *device
	rc      *stubContext
	project string
}

// newHarness starts the SSH server.
func newHarness(t *testing.T) *harness {
	t.Helper()
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return &harness{
		device: &device{
			Stub: &inventorytest.Stub{StubID: "build-1", StubName: "build-box", StubState: inventory.StateActive,
				Caps: []capability.Name{capability.NameSSHTransport, capability.NameCommandExec}},
			host: srv.Host, port: srv.Port,
		},
		rc:      &stubContext{secrets: map[string]string{"username": srv.Username, "password": srv.Password}, stats: map[string]any{}},
		project: t.TempDir(),
	}
}

// write writes content to name in the project with mode, returning its path.
func (h *harness) write(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(h.project, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// params builds a task's parameters with extra merged in.
func params(extra map[string]any) map[string]any {
	p := map[string]any{"insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}
