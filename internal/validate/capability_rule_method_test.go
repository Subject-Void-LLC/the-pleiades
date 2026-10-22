// Tests for CapabilityRule's Collection method half, against throwaway
// methods that require capabilities the way real ones do.
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

// registerCapabilityMethods registers the throwaway methods these tests
// target and restores the registry afterward. They are declared, not
// implemented: the rule reads only a method's manifest.
func registerCapabilityMethods(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	for _, d := range []collection.Descriptor{
		{Name: "test.needs_systemd", Manifest: collection.Manifest{RequiredCapabilities: []capability.Name{capability.NameSystemd}}},
		{Name: "test.needs_a_service_manager", Manifest: collection.Manifest{RequiredCapabilities: []capability.Name{capability.NameServiceManager}}},
		{Name: "test.needs_nothing", Manifest: collection.Manifest{}},
		{
			Name: "test.device_optional_needs_systemd",
			Manifest: collection.Manifest{
				RequiredCapabilities: []capability.Name{capability.NameSystemd},
				ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceOptional},
			},
			// A call with a "path" acts on a device; one without does not.
			DeviceCall: func(params map[string]any) bool { _, ok := params["path"]; return ok },
		},
	} {
		d.Manifest.Status = collection.StatusDeclared
		collection.MustRegister(d)
	}
}

// capabilityWorld is one task calling fqcn with params, under a runbook
// whose hosts: is "lab", against a Linux server with systemd and a Cisco
// switch, both tagged lab.
func capabilityWorld(fqcn string, params map[string]any) validate.WorldView {
	linux := &inventorytest.Stub{StubName: "web1", StubTags: []inventory.Tag{"lab"}, StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameSSHTransport, capability.NameSystemd}}
	cisco := &inventorytest.Stub{StubName: "sw1", StubTags: []inventory.Tag{"lab"}, StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameSSHTransport, capability.NameCiscoIOS}}
	return validate.WorldView{
		Items: []inventory.InventoryItem{linux, cisco},
		DAG: &engine.DAG{
			ID:        "t",
			Hosts:     "lab",
			Nodes:     map[string]*engine.Task{"tasks[0]": {Name: "restart it", FQCN: fqcn, Params: params}},
			Adjacency: map[string][]engine.EdgeConfig{},
		},
	}
}

// TestCapabilityRule_RefusesADeviceWithoutAMethodsCapability is the
// method half's release case: a method requiring SystemdCapable under a
// hosts: that includes a Cisco switch is refused for the switch, naming the
// capability and the device, and the Linux server beside it is not. Before
// Phase 117b only ssh_exec and ios_backup were checked here, and this
// validated cleanly.
func TestCapabilityRule_RefusesADeviceWithoutAMethodsCapability(t *testing.T) {
	registerCapabilityMethods(t)

	findings := validate.CapabilityRule(capabilityWorld("test.needs_systemd", nil))
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one, for sw1", findings)
	}
	f := findings[0]
	if f.RuleName != "capability" || f.Node != "tasks[0]" {
		t.Errorf("finding = %+v, want rule capability on tasks[0]", f)
	}
	for _, want := range []string{`requires capability SystemdCapable, which device "sw1" does not have`, `task tasks[0] (name "restart it")`} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q does not contain %q", f.Message, want)
		}
	}
}

// TestCapabilityRule_AdmitsWhatAMethodsDevicesCarry covers everything the
// method half must leave alone.
func TestCapabilityRule_AdmitsWhatAMethodsDevicesCarry(t *testing.T) {
	registerCapabilityMethods(t)

	for name, tc := range map[string]struct {
		fqcn   string
		params map[string]any
	}{
		"a method requiring nothing":                   {"test.needs_nothing", nil},
		"a method under a target naming only a device": {"test.needs_systemd", map[string]any{"target": "web1"}},
		"a parent capability, satisfied by its child":  {"test.needs_a_service_manager", map[string]any{"target": "web1"}},
		"a device-optional call that acts on a device": {"test.device_optional_needs_systemd", map[string]any{"path": "/x", "target": "web1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if findings := validate.CapabilityRule(capabilityWorld(tc.fqcn, tc.params)); len(findings) != 0 {
				t.Fatalf("findings = %+v, want none", findings)
			}
		})
	}
}

// TestCapabilityRule_ACallWithNoDeviceCannotSupplyACapability: a call that
// runs with no device has nothing to carry the capability its method
// requires, and is refused at plan time as the engine refuses it at run
// time.
func TestCapabilityRule_ACallWithNoDeviceCannotSupplyACapability(t *testing.T) {
	registerCapabilityMethods(t)

	findings := validate.CapabilityRule(capabilityWorld("test.device_optional_needs_systemd", map[string]any{"url": "https://example.test/"}))
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "no target device") {
		t.Fatalf("findings = %+v, want one refusal for the device-less call", findings)
	}
}

// TestCapabilityRule_MethodHalfRunsInValidate proves Validate reports the
// method half, so pleiades validate, run and adhoc, and the Runner's
// preflight, all do.
func TestCapabilityRule_MethodHalfRunsInValidate(t *testing.T) {
	registerCapabilityMethods(t)

	report := validate.Validate(capabilityWorld("test.needs_systemd", nil))
	if !report.HasErrors() || !strings.Contains(report.String(), `which device "sw1" does not have`) {
		t.Fatalf("report = %s, want the capability finding", report.String())
	}
}

// osFamilyRunbook chooses one of two methods by a variable, each under a
// condition of its own, the way a playbook picks apt or dnf by OS family:
// first is the method for systemd hosts, second the one for the others.
func osFamilyRunbook(first, second string) string {
	return `id: os-family
hosts: lab
tasks:
  - name: on systemd
    ` + first + `: {}
    when: vars.family == 'systemd'
  - name: elsewhere
    ` + second + `: {}
    when: vars.family != 'systemd'
`
}

// TestCapabilityRule_LeavesAConditionalTaskToTheEngine: a task with a
// condition of its own may be skipped on any device, and the engine checks
// it only where the condition holds, so refusing it here refused a runbook
// that never calls a method where it cannot. An unconditional task beside
// it is still checked. The runbook is built by the real builder, so the
// condition is the one a run compiles.
func TestCapabilityRule_LeavesAConditionalTaskToTheEngine(t *testing.T) {
	registerCapabilityMethods(t)

	world := capabilityWorld("test.needs_systemd", nil)
	world.DAG = buildYAML(t, osFamilyRunbook("test.needs_systemd", "test.needs_nothing"))
	if findings := validate.CapabilityRule(world); len(findings) != 0 {
		t.Fatalf("findings = %+v, want none: the task is conditional", findings)
	}

	world.DAG = buildYAML(t, `id: plain
hosts: lab
tasks:
  - name: always
    test.needs_systemd: {}
`)
	if findings := validate.CapabilityRule(world); len(findings) != 1 || !strings.Contains(findings[0].Message, `device "sw1"`) {
		t.Fatalf("findings = %+v, want sw1 refused for the unconditional task", findings)
	}
}

// TestCapabilityRule_AConditionalLegacyTargetMustStillExist: the exemption
// covers what a device must carry, not a target naming nothing, which is a
// mistake whatever the condition says.
func TestCapabilityRule_AConditionalLegacyTargetMustStillExist(t *testing.T) {
	world := capabilityWorld("noop", nil)
	world.DAG = buildYAML(t, `id: legacy
tasks:
  - name: nowhere
    ssh_exec:
      target: no-such-host
      command: uptime
    when: vars.go == true
`)
	findings := validate.CapabilityRule(world)
	if len(findings) != 1 || !strings.Contains(findings[0].Message, `target "no-such-host" matches no inventory host or tag`) {
		t.Fatalf("findings = %+v, want the target refused", findings)
	}

	// A legacy action's capability is left to the run like a method's:
	// web1 is no Cisco device, and the backup runs only where the
	// condition holds.
	world.DAG = buildYAML(t, `id: legacy
tasks:
  - name: back it up
    ios_backup:
      target: web1
    when: vars.go == true
`)
	if findings := validate.CapabilityRule(world); len(findings) != 0 {
		t.Fatalf("findings = %+v, want none: the backup is conditional", findings)
	}
}
