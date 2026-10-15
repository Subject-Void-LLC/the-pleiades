// The harness gate.run's and gate.list's tests run against: a real SSH
// server in this process handing each command to a real /bin/sh on this
// machine (pkg/remoteexec/remoteexectest), so the timeout command, a
// directory, an exit status and a process ID behave as they do on a
// device. cmd/pleiades's gate release gate is the proof against a real
// sshd and a real container engine.
package gate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// sshDevice is a device reachable over SSH that runs commands.
type sshDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *sshDevice) SSHHost() string          { return d.host }
func (d *sshDevice) SSHPort() int             { return d.port }
func (d *sshDevice) WorkingDirectory() string { return "" }

// stubContext is a RunbookContext holding the harness's credential and
// keeping every return a method sets.
type stubContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *stubContext) InjectSecrets() map[string]string    { return c.secrets }
func (c *stubContext) SetStat(key string, value any) error { c.stats[key] = value; return nil }
func (c *stubContext) EmitFact(key string, value any) error {
	return c.SetStat(key, value)
}

// harness is one test's SSH server, device, context and project directory.
type harness struct {
	device  *sshDevice
	rc      *stubContext
	project string
}

// newHarness starts the SSH server and a project directory.
func newHarness(t *testing.T, caps ...capability.Name) *harness {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return &harness{
		device: &sshDevice{
			Stub: &inventorytest.Stub{
				StubID: "gate-1", StubName: "gate-box", StubState: inventory.StateActive,
				Caps: append([]capability.Name{capability.NameSSHTransport, capability.NameCommandExec}, caps...),
			},
			host: srv.Host, port: srv.Port,
		},
		rc:      &stubContext{secrets: map[string]string{"username": srv.Username, "password": srv.Password}, stats: map[string]any{}},
		project: t.TempDir(),
	}
}

// manifest writes body as the project's gates.yaml and returns its path.
func (h *harness) manifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(h.project, "gates.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// params is a task's parameters naming the manifest, with extra merged in.
func params(manifest string, extra map[string]any) map[string]any {
	p := map[string]any{"manifest": manifest, "insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// resultsByName reads the checks return into a map by check name.
func resultsByName(t *testing.T, rc *stubContext) map[string]map[string]any {
	t.Helper()
	checks, ok := rc.stats["checks"].([]any)
	if !ok {
		t.Fatalf("no checks return: %v", rc.stats)
	}
	byName := map[string]map[string]any{}
	for _, c := range checks {
		m := c.(map[string]any)
		byName[m["name"].(string)] = m
	}
	return byName
}
