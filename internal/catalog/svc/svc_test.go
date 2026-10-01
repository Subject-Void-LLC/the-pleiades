package svc_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc"

	// Blank-imported so the concrete methods this dispatcher resolves to
	// are actually in the registry. Without it every test here would fail
	// with "svc.systemd.start is not registered", which is a true
	// statement about the test binary rather than about the dispatcher.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/systemd"

	// Also blank-imported, so TestDispatchesToWindows resolves against the
	// real svc.windows.* registrations rather than "not registered", which
	// is a different refusal from the one a real binary produces.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/windows"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
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

// device is a target that reports a service manager by name and is
// reachable over SSH. manager is what ServiceManagerName returns, which
// is the value the whole dispatch turns on.
type device struct {
	*inventorytest.Stub
	host    string
	port    int
	manager string
}

func (d *device) SSHHost() string            { return d.host }
func (d *device) SSHPort() int               { return d.port }
func (d *device) ServiceManagerName() string { return d.manager }
func (d *device) SystemdUnitPath() string    { return "/etc/systemd/system" }

// WinRMHost and WinRMPort let this same device type also stand in for a
// windows_scm target: TestDispatchesToWindows points host/port at an
// address nothing answers, so dispatch resolving to the real
// svc.windows.* implementation surfaces as a dial failure naming the
// concrete FQCN, not a capability or parameter refusal.
func (d *device) WinRMHost() string { return d.host }
func (d *device) WinRMPort() int    { return d.port }

// plainDevice reports no service manager at all: it satisfies neither
// ServiceManagerCapable nor anything below it.
type plainDevice struct{ *inventorytest.Stub }

type harness struct {
	rc     *ctxStub
	dev    *device
	record string
}

// newHarness wires a real SSH server and a fake systemctl reporting a
// stopped, disabled unit, so a start has something real to do.
func newHarness(t *testing.T, manager string, caps ...capability.Name) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	script := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
if [ "$1" = "show" ]; then
  printf 'LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\n'
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake systemctl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	if len(caps) == 0 {
		caps = []capability.Name{capability.NameSystemd}
	}
	// The harness device is an SSH server, so it declares what a real
	// SSH-reached device type declares: the concrete svc.systemd.* methods
	// reach their device over ssh, and dispatch holds it to that.
	caps = append(caps, capability.NameSSHTransport)
	return &harness{
		rc:     &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}},
		dev:    &device{Stub: &inventorytest.Stub{StubName: "web1", Caps: caps}, host: srv.Host, port: srv.Port, manager: manager},
		record: record,
	}
}

func params() map[string]any {
	return map[string]any{"name": "nginx", "insecure_skip_host_key_verify": true}
}

func (h *harness) verbs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(h.record) // #nosec G304 -- path built by this test
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading recorded invocations: %v", err)
	}
	var verbs []string
	first := true
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "---" {
			first = true
			continue
		}
		if first {
			first = false
			if line != "show" {
				verbs = append(verbs, line)
			}
		}
	}
	return verbs
}

// ---------- registration ----------

// TestAllFiveAreImplemented checks the live registry rather than a list,
// including that each requires the BROAD capability. Requiring a concrete
// one here would defeat the entire point of the generic method.
func TestAllFiveAreImplemented(t *testing.T) {
	for _, fqcn := range []string{"svc.start", "svc.stop", "svc.restart", "svc.enable", "svc.disable"} {
		t.Run(fqcn, func(t *testing.T) {
			desc, ok := collection.Lookup(fqcn)
			if !ok {
				t.Fatalf("%s is not registered", fqcn)
			}
			if desc.Manifest.Status != collection.StatusImplemented || desc.Invoke == nil {
				t.Fatalf("Status = %v, Invoke nil = %v; want implemented with an implementation",
					desc.Manifest.Status, desc.Invoke == nil)
			}
			caps := desc.Manifest.RequiredCapabilities
			if len(caps) != 1 || caps[0] != capability.NameServiceManager {
				t.Errorf("RequiredCapabilities = %v, want [ServiceManagerCapable]: a concrete capability here would stop the generic method working on any other platform", caps)
			}
		})
	}
}

// ---------- dispatch ----------

// TestDispatchesToSystemd is the whole contract: the generic method
// resolves the device's manager and the concrete method does the work.
//
// It asserts on the systemctl verb that reached the far end, because that
// is the only evidence that distinguishes real dispatch from a generic
// method that quietly did nothing and reported success.
func TestDispatchesToSystemd(t *testing.T) {
	tests := []struct {
		name     string
		invoke   collection.Method
		wantVerb string
	}{
		{name: "start", invoke: svc.Start, wantVerb: "start"},
		{name: "stop", invoke: svc.Stop, wantVerb: ""}, // already inactive, so converged
		{name: "restart", invoke: svc.Restart, wantVerb: "restart"},
		{name: "enable", invoke: svc.Enable, wantVerb: "enable"},
		{name: "disable", invoke: svc.Disable, wantVerb: ""}, // already disabled
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "systemd")

			if _, err := tt.invoke(context.Background(), h.rc, h.dev, params()); err != nil {
				t.Fatalf("invoke: %v", err)
			}
			got := strings.Join(h.verbs(t), ",")
			if got != tt.wantVerb {
				t.Errorf("systemctl verb sent = %q, want %q", got, tt.wantVerb)
			}
		})
	}
}

// TestDispatchPreservesConvergenceAndInverse proves the generic method
// inherits the concrete one's behavior rather than reimplementing a
// weaker version: a real change reports changed and records an inverse
// naming the CONCRETE method.
func TestDispatchPreservesConvergenceAndInverse(t *testing.T) {
	h := newHarness(t, "systemd")

	result, err := svc.Start(context.Background(), h.rc, h.dev, params())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !result.Changed {
		t.Error("Changed = false for a start against a stopped unit")
	}

	raw, ok := h.rc.stats[sdk.StatInverse]
	if !ok {
		t.Fatal("no inverse recorded")
	}
	inv, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("inverse is %T, want map[string]any", raw)
	}
	// The concrete name, not svc.stop: by the time an undo runs, the
	// device that was resolved is the device that must be undone, and
	// re-resolving could land somewhere else.
	if inv["fqcn"] != "svc.systemd.stop" {
		t.Errorf("inverse fqcn = %v, want svc.systemd.stop", inv["fqcn"])
	}
}

// ---------- refusals ----------

// TestUnknownServiceManagerIsRefused covers the OpenRC and Alpine case.
// A Linux device type declares SystemdCapable in its baseline, and the
// service_manager property is how a host that does not run systemd says
// so; this is what makes that property mean something.
func TestUnknownServiceManagerIsRefused(t *testing.T) {
	h := newHarness(t, "openrc")

	_, err := svc.Start(context.Background(), h.rc, h.dev, params())
	if err == nil {
		t.Fatal("expected a refusal for a service manager with no methods")
	}
	for _, want := range []string{"openrc", "systemd", "windows_scm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q, both what was found and what is known", err, want)
		}
	}
	if verbs := h.verbs(t); len(verbs) != 0 {
		t.Errorf("commands were sent (%v) despite an unresolvable service manager", verbs)
	}
}

// TestDispatchesToWindows is svc.windows.*'s own version of
// TestDispatchesToSystemd, once that namespace stopped being declared and
// became a real implementation: proof that a windows_scm device resolves
// through the generic dispatcher to the concrete svc.windows.* method
// rather than stopping short at "not registered" or a capability refusal.
//
// There is no in-process WinRM server to run the real command against
// (the identical constraint pkg/winrmexec's own tests document), so this
// points the device at an address nothing answers and asserts on the
// FAILURE MODE: an error naming the concrete FQCN and reaching the
// network is only possible if dispatch actually resolved and invoked
// windows.Start, not if it had refused earlier on a missing registration
// or a missing capability.
func TestDispatchesToWindows(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{"username": "administrator", "password": "secret"}, stats: map[string]any{}}
	dev := &device{
		Stub:    &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWindowsService, capability.NameWinRM}},
		host:    "127.0.0.1",
		port:    1,
		manager: "windows_scm",
	}

	_, err := svc.Start(context.Background(), rc, dev, params())
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "svc.windows.start") {
		t.Errorf("error = %v, want it to name the concrete FQCN dispatch resolved to", err)
	}
	if strings.Contains(err.Error(), "not registered") || strings.Contains(err.Error(), "declared but not implemented") ||
		strings.Contains(err.Error(), "reaches its device over") {
		t.Errorf("error = %v, want a real dispatch attempt, not an early refusal", err)
	}
}

// TestDispatchHoldsTheDeviceToTheConcreteTransport proves dispatch
// checks the concrete method's transports, not only the generic one's.
// svc.start declares ssh and winrm, because its concrete methods speak
// one each, so the engine admits a device reaching either; a device that
// reports systemd while reachable only over WinRM must be refused before
// anything is sent, naming the transport svc.systemd.start needs.
func TestDispatchHoldsTheDeviceToTheConcreteTransport(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
	dev := &device{
		Stub:    &inventorytest.Stub{StubName: "odd1", Caps: []capability.Name{capability.NameSystemd, capability.NameWinRM}},
		host:    "127.0.0.1",
		port:    1,
		manager: "systemd",
	}

	_, err := svc.Start(context.Background(), rc, dev, params())
	if err == nil || !strings.Contains(err.Error(), `"svc.systemd.start" reaches its device over ssh`) ||
		!strings.Contains(err.Error(), "reaches only winrm") {
		t.Fatalf("err = %v, want svc.systemd.start refused for a device reaching only winrm", err)
	}
}

// TestDeviceWithNoServiceManagerIsRefused covers a device that satisfies
// nothing this can dispatch on.
func TestDeviceWithNoServiceManagerIsRefused(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
	dev := &plainDevice{Stub: &inventorytest.Stub{StubName: "switch1"}}

	_, err := svc.Start(context.Background(), rc, dev, params())
	if err == nil {
		t.Fatal("expected a refusal for a device that reports no service manager")
	}
	if !strings.Contains(err.Error(), "switch1") {
		t.Errorf("error = %v, want it to name the device", err)
	}
}

// TestNilDeviceIsRefused proves the dispatcher does not panic on a
// targetless task, which it would if it type-asserted a nil interface
// without checking.
func TestNilDeviceIsRefused(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}

	_, err := svc.Start(context.Background(), rc, nil, params())
	if err == nil {
		t.Fatal("expected a refusal for a nil device")
	}
	if !strings.Contains(err.Error(), "target device") {
		t.Errorf("error = %v, want it to say a target device is needed", err)
	}
}

// TestConcreteCapabilityIsStillChecked closes the hole invoking a
// descriptor directly would otherwise open.
//
// engine.checkMethodCapabilities validates the GENERIC method's
// requirement, ServiceManagerCapable, before dispatch. Calling
// desc.Invoke bypasses the executor, so if this did not check the
// concrete method's stricter requirement, nothing ever would. The device
// here reports systemd and declares only the parent capability.
func TestConcreteCapabilityIsStillChecked(t *testing.T) {
	h := newHarness(t, "systemd", capability.NameServiceManager)

	_, err := svc.Start(context.Background(), h.rc, h.dev, params())
	if err == nil {
		t.Fatal("expected a refusal: the device does not have SystemdCapable, which svc.systemd.start requires")
	}
	if !strings.Contains(err.Error(), string(capability.NameSystemd)) {
		t.Errorf("error = %v, want it to name the capability the concrete method requires", err)
	}
	if verbs := h.verbs(t); len(verbs) != 0 {
		t.Errorf("commands were sent (%v) despite the capability check failing", verbs)
	}
}

// TestDeviceInterfaceIsSatisfied guards the test double itself: if
// *device stopped satisfying these, the dispatch tests above would fail
// for a reason that has nothing to do with the dispatcher.
func TestDeviceInterfaceIsSatisfied(t *testing.T) {
	var d inventory.InventoryItem = &device{Stub: &inventorytest.Stub{StubName: "x"}}
	if _, ok := d.(capability.ServiceManagerCapable); !ok {
		t.Error("the test device does not satisfy ServiceManagerCapable")
	}
	if _, ok := d.(capability.SystemdCapable); !ok {
		t.Error("the test device does not satisfy SystemdCapable")
	}
}
