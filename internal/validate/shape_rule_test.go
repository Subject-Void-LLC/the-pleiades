// Tests that the capability and transport rules hold a call to the shape
// its parameters select (collection.Manifest.For), as the engine does.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// registerShapedMethod registers a method shaped as net.trace is: Linux
// over SSH, or, given container, a container engine.
func registerShapedMethod(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	collection.MustRegister(collection.Descriptor{Name: "test.shaped", Manifest: collection.Manifest{
		Status:               collection.StatusDeclared,
		SupportedTransports:  []string{capability.TransportSSH},
		RequiredCapabilities: []capability.Name{capability.NameSystemd},
		Shapes: []collection.CallShape{{
			Param:                "container",
			SupportedTransports:  capability.EngineTransports(),
			RequiredCapabilities: []capability.Name{capability.NameContainerEngine},
		}},
		Doc: collection.Doc{Params: []collection.Param{{Name: "container", Type: "string", Description: "x"}}},
	}})
}

// TestRules_HoldACallToItsShape: without container the call is the
// manifest's own shape, so the switch lacks SystemdCapable and the Linux
// server passes; with container it is the engine's shape, so both lack
// ContainerEngineCapable and reach no engine transport, and neither is
// refused for SystemdCapable.
func TestRules_HoldACallToItsShape(t *testing.T) {
	registerShapedMethod(t)

	plain := capabilityWorld("test.shaped", nil)
	if f := validate.CapabilityRule(plain); len(f) != 1 || !strings.Contains(f[0].Message, "SystemdCapable") || !strings.Contains(f[0].Message, `"sw1"`) {
		t.Errorf("a plain call's capability findings: %+v", f)
	}
	if f := validate.TransportRule(plain); len(f) != 0 {
		t.Errorf("a plain call's transport findings: %+v", f)
	}

	shaped := capabilityWorld("test.shaped", map[string]any{"container": "h1"})
	caps := validate.CapabilityRule(shaped)
	if len(caps) != 2 {
		t.Fatalf("a container call's capability findings: %+v, want one per device", caps)
	}
	for _, f := range caps {
		if !strings.Contains(f.Message, "ContainerEngineCapable") || strings.Contains(f.Message, "SystemdCapable") {
			t.Errorf("finding %q", f.Message)
		}
	}
	if f := validate.TransportRule(shaped); len(f) != 2 || !strings.Contains(f[0].Message, "engine_local") {
		t.Errorf("a container call's transport findings: %+v", f)
	}
	if routes := validate.EngineRoutes(shaped); len(routes) != 0 {
		t.Errorf("routes to engines neither device has: %+v", routes)
	}
}
