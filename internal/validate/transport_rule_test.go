// Tests for TransportRule, against throwaway methods that declare
// transports the way real ones do.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// registerTransportMethods registers the throwaway methods these tests
// target, each declaring transports the way a real one does, and restores
// the registry afterward. They are declared, not implemented: the rule
// reads only a method's manifest.
func registerTransportMethods(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	for _, d := range []collection.Descriptor{
		{Name: "test.ssh_only", Manifest: collection.Manifest{SupportedTransports: []string{capability.TransportSSH}}},
		{Name: "test.either", Manifest: collection.Manifest{SupportedTransports: []string{capability.TransportSSH, capability.TransportWinRM}}},
		{Name: "test.api", Manifest: collection.Manifest{}},
		{
			Name: "test.sometimes_a_device",
			Manifest: collection.Manifest{
				SupportedTransports: []string{capability.TransportHTTPS},
				ExecutionContext:    collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceOptional},
			},
			// A call with a "path" acts on a device; one without does not.
			DeviceCall: func(params map[string]any) bool { _, ok := params["path"]; return ok },
		},
	} {
		d.Manifest.Status = collection.StatusDeclared
		collection.MustRegister(d)
	}
}

// transportWorld is one task calling fqcn with params, under a runbook
// whose hosts: is "lab", against a Windows server reachable only over
// WinRM and a Linux server reachable only over SSH, both tagged lab.
func transportWorld(fqcn string, params map[string]any) validate.WorldView {
	windows := &inventorytest.Stub{StubName: "win1", StubTags: []inventory.Tag{"lab"}, StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameWinRM, capability.NameWindowsShell}}
	linux := &inventorytest.Stub{StubName: "web1", StubTags: []inventory.Tag{"lab"}, StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameSSHTransport, capability.NameShellExec}}
	return validate.WorldView{
		Items: []inventory.InventoryItem{windows, linux},
		DAG: &engine.DAG{
			ID:        "t",
			Hosts:     "lab",
			Nodes:     map[string]*engine.Task{"tasks[0]": {Name: "do it", FQCN: fqcn, Params: params}},
			Adjacency: map[string][]engine.EdgeConfig{},
		},
	}
}

// TestTransportRule_RefusesADeviceTheMethodCannotReach is the rule's
// release case: an SSH-only method under a hosts: that includes a
// WinRM-only Windows server is refused for that device, naming the
// transports on both sides, and the Linux server beside it is not.
func TestTransportRule_RefusesADeviceTheMethodCannotReach(t *testing.T) {
	registerTransportMethods(t)

	findings := validate.TransportRule(transportWorld("test.ssh_only", nil))
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one, for win1", findings)
	}
	f := findings[0]
	if f.RuleName != "transport" || f.Node != "tasks[0]" {
		t.Errorf("finding = %+v, want rule transport on tasks[0]", f)
	}
	for _, want := range []string{`"test.ssh_only" reaches its device over ssh`, `device "win1" reaches only winrm`, `task tasks[0] (name "do it")`} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q does not contain %q", f.Message, want)
		}
	}
}

// TestTransportRule_AdmitsWhatTheMethodCanReach covers everything the
// rule must leave alone.
func TestTransportRule_AdmitsWhatTheMethodCanReach(t *testing.T) {
	registerTransportMethods(t)

	for name, tc := range map[string]struct {
		fqcn   string
		params map[string]any
	}{
		"either transport reaches both devices":        {"test.either", nil},
		"a method declaring no transport":              {"test.api", nil},
		"a call that acts on no device":                {"test.sometimes_a_device", map[string]any{"url": "https://example.test/"}},
		"a name that is not a Collection method":       {"noop", nil},
		"a method under a target naming only a device": {"test.ssh_only", map[string]any{"target": "web1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if findings := validate.TransportRule(transportWorld(tc.fqcn, tc.params)); len(findings) != 0 {
				t.Fatalf("findings = %+v, want none", findings)
			}
		})
	}
}

// TestTransportRule_ACallThatActsOnADeviceIsChecked proves an optional
// device is checked on the calls that use one.
func TestTransportRule_ACallThatActsOnADeviceIsChecked(t *testing.T) {
	registerTransportMethods(t)

	findings := validate.TransportRule(transportWorld("test.sometimes_a_device", map[string]any{"path": "/api"}))
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want both devices refused: neither reaches https", findings)
	}
}

// TestTransportRule_IsRegistered proves Validate runs the rule, so
// pleiades validate, run and adhoc, and the Runner's preflight, all do.
func TestTransportRule_IsRegistered(t *testing.T) {
	registerTransportMethods(t)

	report := validate.Validate(transportWorld("test.ssh_only", nil))
	if !report.HasErrors() || !strings.Contains(report.String(), `device "win1" reaches only winrm`) {
		t.Fatalf("report = %s, want the transport finding", report.String())
	}
}
