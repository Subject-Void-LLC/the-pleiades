// The harness the scan methods' tests run against: a real SSH server in
// this process whose commands run through a real /bin/sh on this machine,
// and this machine's own Docker engine, on which the real pinned scanners
// run against real files. Nothing reaches the network: OSV-Scanner reads
// a local database the test writes.
package scan_test

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// dockerSocket is this machine's Docker engine.
const dockerSocket = "/var/run/docker.sock"

// device is an SSH device that runs commands, whose engine is this
// machine's.
type device struct {
	*inventorytest.Stub
	host   string
	port   int
	id     capability.EngineID
	user   string
	engine bool
}

func (d *device) SSHHost() string          { return d.host }
func (d *device) SSHPort() int             { return d.port }
func (d *device) WorkingDirectory() string { return "" }
func (d *device) ContainerEngines() []capability.EngineKind {
	return []capability.EngineKind{capability.EngineDocker}
}
func (d *device) DockerEndpoint() capability.SocketAddress                    { return dockerSocket }
func (d *device) EngineSocket(capability.EngineKind) capability.SocketAddress { return dockerSocket }
func (d *device) EngineID(capability.EngineKind) capability.EngineID          { return d.id }
func (d *device) EngineCarriers(capability.EngineKind) []capability.Carrier {
	return []capability.Carrier{capability.CarrierLocal}
}
func (d *device) EngineLoginUser() string { return d.user }

// stubContext keeps every return a method sets.
type stubContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *stubContext) InjectSecrets() map[string]string     { return c.secrets }
func (c *stubContext) SetStat(key string, value any) error  { c.stats[key] = value; return nil }
func (c *stubContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// harness is one test's device and run.
type harness struct {
	device *device
	rc     *stubContext
}

// newHarness starts the SSH server, with this machine's engine as the
// device's, skipping where there is no Docker.
func newHarness(t *testing.T) *harness {
	t.Helper()
	if testing.Short() {
		t.Skip("runs the real scanners on this machine's Docker engine")
	}
	out, err := exec.Command("docker", "info", "--format", "{{.ID}}").Output()
	testsupport.Require(t, "docker", err == nil && len(strings.TrimSpace(string(out))) > 0, "the scanners run on this machine's Docker engine")
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	user, err := containerengine.LocalUser()
	if err != nil {
		t.Fatal(err)
	}
	// The engine is this machine's socket, which only the command line may
	// reach; these tests stand where it does.
	containerengine.AllowLocalCarrier()
	return &harness{
		device: &device{
			Stub: &inventorytest.Stub{StubID: "scan-1", StubName: "scan-box", StubState: inventory.StateActive,
				Caps: []capability.Name{capability.NameSSHTransport, capability.NameCommandExec, capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal}},
			host: srv.Host, port: srv.Port, id: capability.EngineID(strings.TrimSpace(string(out))), user: user, engine: true,
		},
		rc: &stubContext{secrets: map[string]string{"username": srv.Username, "password": srv.Password}, stats: map[string]any{}},
	}
}

// fresh returns a new stats map for the next call.
func (h *harness) fresh() *stubContext {
	h.rc.stats = map[string]any{}
	return h.rc
}

// params builds a task's parameters with extra merged in.
func params(extra map[string]any) map[string]any {
	p := map[string]any{"insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// writeFiles writes files into a new directory, readable by the scanners'
// containers, and returns it.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// npmProject is a project whose lockfile holds left-pad 1.3.0.
var npmProject = map[string]string{
	"package.json":      `{"name":"demo","version":"1.0.0","dependencies":{"left-pad":"^1.3.0"}}`,
	"package-lock.json": `{"name":"demo","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"demo","version":"1.0.0","dependencies":{"left-pad":"^1.3.0"}},"node_modules/left-pad":{"version":"1.3.0","resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz","integrity":"sha512-XI5MPzVNApjAyhQzphX8BkmKsKUxD4LdyK24iZeQEBAhFFX6+AGJCqp7KvP5+WmIp5Ud71wOdvFE/tDHg6P2A=="}}}`,
}

// offlineDB writes an OSV-Scanner local database holding advisories (OSV
// JSON, by id) for npm, in the layout OSV-Scanner 2 reads.
func offlineDB(t *testing.T, advisories map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	eco := filepath.Join(dir, "osv-scalibr", "npm")
	if err := os.MkdirAll(eco, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dir, filepath.Join(dir, "osv-scalibr")} {
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(filepath.Join(eco, "all.zip")) // #nosec G304 -- this test's own directory
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for id, body := range advisories {
		w, err := z.Create(id + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Chmod(0o644); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return dir
}

// advisory is an OSV record for npm left-pad below fixed (none when
// empty), with a CVSS v3 vector (none when empty).
func advisory(id, fixed, vector string) string {
	events := `{"introduced":"0"}`
	if fixed != "" {
		events += `,{"fixed":"` + fixed + `"}`
	}
	severity := ""
	if vector != "" {
		severity = `,"severity":[{"type":"CVSS_V3","score":"` + vector + `"}]`
	}
	return `{"schema_version":"1.6.0","id":"` + id + `","modified":"2026-01-01T00:00:00Z","published":"2026-01-01T00:00:00Z","summary":"test","affected":[{"package":{"ecosystem":"npm","name":"left-pad"},"ranges":[{"type":"SEMVER","events":[` + events + `]}]}]` + severity + `}`
}

// The vectors the tests rate.
const (
	criticalVector = "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"
	mediumVector   = "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:L/I:L/A:N"
)
