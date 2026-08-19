package pkg_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg"

	// Blank-imported so the concrete methods this dispatcher resolves to
	// are actually in the registry. Without these, every test here would
	// fail with "pkg.apt.install is not registered", a true statement
	// about the test binary rather than about the dispatcher.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/dnf"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// ---------- harness ----------

type ctxStub struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *ctxStub) InjectSecrets() map[string]string { return c.secrets }
func (c *ctxStub) SetStat(key string, value any) error {
	c.stats[key] = value
	return nil
}
func (c *ctxStub) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// device is a target that reports a package manager by name and is
// reachable over SSH. manager is what PackageManagerName returns, which
// is the value the whole dispatch turns on.
type device struct {
	*inventorytest.Stub
	host    string
	port    int
	manager string
}

func (d *device) SSHHost() string            { return d.host }
func (d *device) SSHPort() int               { return d.port }
func (d *device) PackageManagerName() string { return d.manager }

// plainDevice reports no package manager at all: it satisfies neither
// PackageManagerCapable nor anything below it.
type plainDevice struct{ *inventorytest.Stub }

// newAptHarness wires a real SSH server, a fake apt-get/dpkg-query
// reporting curl absent, and a device declaring AptCapable, so
// pkg.install has something real to dispatch to and act on.
func newAptHarness(t *testing.T) (*device, *ctxStub, string) {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	writeFakeAptGet(t, dir, record, fakeAptState{})

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	dev := &device{
		Stub: &inventorytest.Stub{
			StubName: "web1",
			Caps:     []capability.Name{capability.NameApt},
		},
		host:    srv.Host,
		port:    srv.Port,
		manager: "apt",
	}
	rc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
	return dev, rc, record
}

// fakeAptState is copied here rather than imported from the apt
// package's own test file: the two fakes drift on purpose, since a
// dispatch-layer test only needs "something real happens," not the
// full state matrix apt's own tests already cover.
type fakeAptState struct {
	installed bool
	version   string
}

func writeFakeAptGet(t *testing.T, dir, record string, state fakeAptState) {
	t.Helper()

	status := "unknown ok not-installed"
	version := ""
	if state.installed {
		status = "install ok installed"
		version = state.version
	}
	script := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
exit 0
`
	dpkgScript := `#!/bin/sh
printf '` + status + `\t` + version + `'
exit ` + boolExit(state.installed) + `
`
	if err := os.WriteFile(filepath.Join(dir, "apt-get"), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake apt-get: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dpkg-query"), []byte(dpkgScript), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake dpkg-query: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
}

func boolExit(installed bool) string {
	if installed {
		return "0"
	}
	return "1"
}

// verbs returns the apt-get verb of every invocation actually sent.
func verbs(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record) // #nosec G304 -- path built by this test
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading recorded invocations: %v", err)
	}
	var out []string
	first := true
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "---" {
			first = true
			continue
		}
		if first {
			first = false
			out = append(out, line)
		}
	}
	return out
}

// ---------- registration ----------

func TestGeneric_Registered(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "pkg.install", want: "pkg.install"},
		{name: "pkg.remove", want: "pkg.remove"},
		{name: "pkg.upgrade", want: "pkg.upgrade"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.want)
			if !ok {
				t.Fatalf("collection.Lookup(%q) found nothing", tc.want)
			}
			if d.Manifest.Status != collection.StatusImplemented {
				t.Errorf("Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
			}
			if d.Invoke == nil {
				t.Error("Invoke is nil")
			}
			if len(d.Manifest.RequiredCapabilities) != 1 || d.Manifest.RequiredCapabilities[0] != capability.NamePackageManager {
				t.Errorf("RequiredCapabilities = %v, want exactly [%s]", d.Manifest.RequiredCapabilities, capability.NamePackageManager)
			}
		})
	}
}

func TestUpgrade_Registered_Irreversible(t *testing.T) {
	d, ok := collection.Lookup("pkg.upgrade")
	if !ok {
		t.Fatal("pkg.upgrade not registered")
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("pkg.upgrade claims reversible, but every concrete method it can resolve to is not")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversible: false with no Notes; collection.Register should have refused this at registration")
	}
}

// ---------- refusals before any real work ----------

func TestDispatch_RefusesNilDevice(t *testing.T) {
	_, err := pkg.Install(context.Background(), &ctxStub{stats: map[string]any{}}, nil, map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), "needs a target device") {
		t.Errorf("err = %v, want it to mention needing a target device", err)
	}
}

func TestDispatch_RefusesADeviceWithNoPackageManager(t *testing.T) {
	dev := &plainDevice{Stub: &inventorytest.Stub{StubName: "mystery1"}}
	_, err := pkg.Install(context.Background(), &ctxStub{stats: map[string]any{}}, dev, map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), "does not report a package manager") {
		t.Errorf("err = %v, want it to mention not reporting a package manager", err)
	}
}

func TestDispatch_RefusesAnUnknownManager(t *testing.T) {
	dev := &device{
		Stub:    &inventorytest.Stub{StubName: "bsd1"},
		manager: "pkgsrc",
	}
	_, err := pkg.Install(context.Background(), &ctxStub{stats: map[string]any{}}, dev, map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), `"pkgsrc"`) || !strings.Contains(err.Error(), "no methods for") {
		t.Errorf("err = %v, want it to name the unknown manager and say it has no methods", err)
	}
}

func TestDispatch_RefusesWhenTheDeviceLacksTheConcreteCapability(t *testing.T) {
	// Reports apt, but was never given AptCapable, so the concrete
	// method's OWN capability requirement (checked inside dispatch, not
	// only by the executor pkg.install's tests bypass) refuses it.
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	dev := &device{
		Stub:    &inventorytest.Stub{StubName: "web1"},
		host:    srv.Host,
		port:    srv.Port,
		manager: "apt",
	}
	rc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}

	_, err = pkg.Install(context.Background(), rc, dev, map[string]any{"name": "curl", "insecure_skip_host_key_verify": true})
	if err == nil || !strings.Contains(err.Error(), "does not have") {
		t.Errorf("err = %v, want it to say the device lacks the capability pkg.apt.install requires", err)
	}
}

// ---------- real dispatch, through a real fake apt-get ----------

func TestDispatch_InstallResolvesToAptAndRuns(t *testing.T) {
	dev, rc, record := newAptHarness(t)

	result, err := pkg.Install(context.Background(), rc, dev, map[string]any{
		"name":                          "curl",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Changed {
		t.Error("expected the absent package to be installed")
	}
	got := verbs(t, record)
	if len(got) == 0 || got[0] != "install" {
		t.Errorf("apt-get verbs = %v, want the first one to be install", got)
	}
	if rc.stats["name"] != "curl" {
		t.Errorf(`stats["name"] = %v, want "curl"`, rc.stats["name"])
	}
}

func TestDispatch_RemoveResolvesToAptAndRuns(t *testing.T) {
	dev, rc, record := newAptHarness(t)
	// This harness's fake dpkg-query always reports curl absent, so
	// remove should converge without sending anything: proving the
	// dispatcher reached the real method AND that method's own
	// converge logic, not just that dispatch happened.
	result, err := pkg.Remove(context.Background(), rc, dev, map[string]any{
		"name":                          "curl",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.Changed {
		t.Error("expected no change: the fake dpkg-query reports curl as already absent")
	}
	if got := verbs(t, record); len(got) != 0 {
		t.Errorf("apt-get verbs = %v, want none sent for an already-converged remove", got)
	}
}

func TestDispatch_UpgradeIsNotReversible(t *testing.T) {
	dev, rc, _ := newAptHarness(t)
	result, err := pkg.Upgrade(context.Background(), rc, dev, map[string]any{
		"name":                          "curl",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !result.Changed {
		t.Error("expected the absent package to be installed by upgrade's own state=latest semantics")
	}
	if _, recorded := rc.stats["inverse"]; recorded {
		t.Error("pkg.upgrade recorded an inverse, but its manifest says Reversible: false")
	}
}
