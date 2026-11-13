// Tests that validation holds a method serving more than one kind of
// device to one of its kinds (collection.Manifest.Alternatives), with the
// real wait.routes as the method.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
)

// routesWorld is a wait.routes task under hosts: lab, over an FRR router,
// a Cisco IOS router reached through a console server, and a Linux server
// that can run commands over SSH but is neither.
func routesWorld() validate.WorldView {
	stub := func(name string, caps ...capability.Name) inventory.InventoryItem {
		return &inventorytest.Stub{StubName: name, StubTags: []inventory.Tag{"lab"}, StubState: inventory.StateActive, Caps: caps}
	}
	return validate.WorldView{
		Items: []inventory.InventoryItem{
			stub("r1", capability.NameFRR, capability.NameCommandExec, capability.NameSSHTransport),
			stub("r2", capability.NameCiscoIOS, capability.NameConsoleServed),
			stub("web1", capability.NameLinux, capability.NameCommandExec, capability.NameSSHTransport),
		},
		DAG: &engine.DAG{
			ID:        "t",
			Hosts:     "lab",
			Nodes:     map[string]*engine.Task{"tasks[0]": {Name: "routes settle", FQCN: "wait.routes", Params: map[string]any{"prefixes": []any{"10.1.0.0/24"}}}},
			Adjacency: map[string][]engine.EdgeConfig{},
		},
	}
}

// TestValidate_RefusesADeviceThatIsNeitherFRRNorIOS: before the change to
// alternatives this runbook validated cleanly and the Linux server failed
// only when the task reached it. Now it is refused at plan time, once,
// naming both kinds, and the two routers beside it are not; the transport
// rule says nothing, since the server reaches SSH.
func TestValidate_RefusesADeviceThatIsNeitherFRRNorIOS(t *testing.T) {
	world := routesWorld()
	findings := validate.CapabilityRule(world)
	if len(findings) != 1 ||
		!strings.Contains(findings[0].Message, `device "web1" is none of them`) ||
		!strings.Contains(findings[0].Message, "frr (FRRCapable and CommandExecCapable, over ssh); ios (CiscoIOSCapable") {
		t.Fatalf("capability findings = %+v, want one for web1 naming both kinds", findings)
	}
	if f := validate.TransportRule(world); len(f) != 0 {
		t.Errorf("transport findings = %+v, want none", f)
	}
	if report := validate.Validate(world); !report.HasErrors() {
		t.Error("the whole validation passed the Linux server")
	}
}
