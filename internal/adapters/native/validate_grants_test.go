// Tests that a Runner's validation of a dispatch returns the grant
// warnings validation gives, and only those.
package native

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// notingDevice is a dispatched device recording grants and what became of
// each, as every device a Runner rebuilds on record.Base does.
type notingDevice struct {
	*inventorytest.Stub
	notes []inventory.GrantNote
}

// GrantNotes implements inventory.GrantNoter.
func (d notingDevice) GrantNotes() []inventory.GrantNote { return d.notes }

// TestValidateDispatch_ReturnsTheGrantWarnings: a dispatch whose device
// records a grant no loaded program defines returns that warning for the
// job's log even when the dispatch is refused for another reason, since a
// lapsed grant is often why. A dispatch that validates returning it is
// TestExternalCapabilityReleaseGate_TheRunnerHoldsAShippedGrant's last case.
func TestValidateDispatch_ReturnsTheGrantWarnings(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	collection.MustRegister(collection.Descriptor{Name: "validategrants.read", Manifest: collection.Manifest{Status: collection.StatusDeclared}})
	device := notingDevice{
		Stub:  &inventorytest.Stub{StubID: "d1", StubName: "w1", StubState: inventory.StateActive, Caps: []capability.Name{capability.NameSSHTransport}},
		notes: []inventory.GrantNote{{Capability: "gone.thing", Lapsed: true, Reason: "device w1's grant of gone.thing grants nothing here: no loaded program defines gone.thing"}},
	}
	dag := func(fqcn string) *engine.DAG {
		return &engine.DAG{ID: "t", Nodes: map[string]*engine.Task{"tasks[0]": {Name: "read", FQCN: fqcn}}, Adjacency: map[string][]engine.EdgeConfig{}}
	}
	for name, fqcn := range map[string]string{"refused, a declared method": "validategrants.read", "refused, an unknown method": "validategrants.nothing"} {
		t.Run(name, func(t *testing.T) {
			warnings, err := validateDispatch(dag(fqcn), device, collection.ModeExecute)
			if err == nil {
				t.Fatal("the dispatch validated, so this case proves nothing about a refused one")
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], "no loaded program defines gone.thing") {
				t.Errorf("warnings %q, want the one grant warning", warnings)
			}
		})
	}
}
