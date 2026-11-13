// Tests for the warnings validation gives about the grants of the devices
// a runbook reaches.
package validate_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// grantNotingStub is a device recording grants and what became of each, as
// every device built on record.Base does.
type grantNotingStub struct {
	*inventorytest.Stub
	notes []inventory.GrantNote
}

// GrantNotes implements inventory.GrantNoter.
func (s grantNotingStub) GrantNotes() []inventory.GrantNote { return s.notes }

// grantWorld is a runbook calling acme.widget.read on w1 (unconditionally,
// or under a condition), and w1 recording four grants: one no loaded
// program defines, one that counts with a warning, one that lapsed of the
// capability the task requires, and one that lapsed of another; and w2,
// which no task reaches, with a lapse of its own.
func grantWorld(t *testing.T, conditional bool) validate.WorldView {
	t.Helper()
	t.Cleanup(capability.SnapshotForTest())
	t.Cleanup(collection.SnapshotForTest())
	for _, name := range []capability.Name{"acme.widget_api", "acme.gadget_api"} {
		if err := capability.RegisterData(capability.DataDefinition{Name: name, Provider: "acme"}); err != nil {
			t.Fatal(err)
		}
	}
	collection.MustRegister(collection.Descriptor{Name: "acme.widget.read", Manifest: collection.Manifest{
		Status: collection.StatusDeclared, RequiredCapabilities: []capability.Name{"acme.widget_api"},
	}})
	w1 := grantNotingStub{Stub: &inventorytest.Stub{StubID: "d1", StubName: "w1", StubState: inventory.StateActive}, notes: []inventory.GrantNote{
		{Capability: "gone.thing", Lapsed: true, Reason: "device w1's grant of gone.thing grants nothing here: no loaded program defines gone.thing"},
		{Capability: "acme.legacy_api", Reason: "device w1's grant of acme.legacy_api predates binding a grant to its capability's definition"},
		{Capability: "acme.widget_api", Lapsed: true, Reason: "program \"acme\"'s definition of acme.widget_api (revision 1) is not the one device w1's grant was made against"},
		{Capability: "acme.gadget_api", Lapsed: true, Reason: "program \"acme\"'s definition of acme.gadget_api (revision 3) is not the one device w1's grant was made against"},
	}}
	w2 := grantNotingStub{Stub: &inventorytest.Stub{StubID: "d2", StubName: "w2", StubState: inventory.StateActive}, notes: []inventory.GrantNote{
		{Capability: "acme.gadget_api", Lapsed: true, Reason: "device w2's grant lapsed"},
	}}
	dag := &engine.DAG{
		ID:        "t",
		Hosts:     "w1",
		Nodes:     map[string]*engine.Task{"tasks[0]": {Name: "read it", FQCN: "acme.widget.read"}},
		Adjacency: map[string][]engine.EdgeConfig{},
	}
	if conditional {
		dag.Conditions = map[string]*engine.ConditionProgram{"tasks[0]": {}}
	}
	return validate.WorldView{Items: []inventory.InventoryItem{w1, w2}, DAG: dag}
}

// messagesOf is each warning's message, on the rule GrantWarnings uses.
func messagesOf(t *testing.T, warnings []validate.Finding) []string {
	t.Helper()
	var out []string
	for _, w := range warnings {
		if w.RuleName != validate.GrantRuleName {
			continue
		}
		if w.Device != "d1" {
			t.Errorf("a grant warning about device %q, which no task reaches", w.Device)
		}
		out = append(out, w.Message)
	}
	return out
}

// TestGrantWarnings_WarnOfEveryGrantThatDoesNotSimplyCount: Validate warns
// of each grant on a device the runbook reaches that grants nothing or
// counts only with a warning, naming it as the grant's note does; it leaves
// out the lapse a task there is held to, which CapabilityRule refuses with
// the same reason, and every grant on a device no task reaches.
func TestGrantWarnings_WarnOfEveryGrantThatDoesNotSimplyCount(t *testing.T) {
	world := grantWorld(t, false)
	report := validate.Validate(world)
	got := messagesOf(t, report.Warnings)
	w1 := world.Items[0].(grantNotingStub).notes
	want := []string{w1[0].Reason, w1[1].Reason, w1[3].Reason}
	if !slices.Equal(got, want) {
		t.Errorf("grant warnings\n%q\nwant\n%q", got, want)
	}
	refused := false
	for _, f := range report.Findings {
		refused = refused || (f.RuleName == "capability" && f.Device == "d1")
	}
	if !refused {
		t.Errorf("findings %+v: the lapsed grant the task needs is not refused, so leaving its warning out hid it", report.Findings)
	}
}

// TestGrantWarnings_ACheckLeftToTheRunStillWarns: a task under a condition
// of its own is not held to its capabilities at plan time
// (CapabilityRule), so the lapse of the capability it requires is warned
// about rather than said nowhere.
func TestGrantWarnings_ACheckLeftToTheRunStillWarns(t *testing.T) {
	got := messagesOf(t, validate.Validate(grantWorld(t, true)).Warnings)
	if len(got) != 4 {
		t.Errorf("grant warnings %q, want all four of w1's", got)
	}
}

// TestGrantWarnings_NoRunbookNoWarnings: a view with no runbook gives
// none.
func TestGrantWarnings_NoRunbookNoWarnings(t *testing.T) {
	if got := validate.GrantWarnings(validate.WorldView{}); len(got) != 0 {
		t.Errorf("GrantWarnings = %+v, want none", got)
	}
}
