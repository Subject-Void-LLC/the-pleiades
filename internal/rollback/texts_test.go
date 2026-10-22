// Tests for a recorded undo parameter that is a list of identifiers, such
// as the packages an install pulled in: planned, verified against the
// runbook that ran, and refused when the list is not one the journal
// would hold.
package rollback_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerInstall registers an install whose undo removes the package and
// the dependencies it lists.
func registerInstall(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	params := []collection.Param{{Name: "name", Type: "string"}, {Name: "deps", Type: "list"}}
	for name, r := range map[string]collection.Reversibility{
		"rbl.install":   {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rbl.uninstall", Record: []string{"name", "deps"}}}},
		"rbl.uninstall": {Notes: "gone is gone"},
	} {
		d := collection.Descriptor{Name: name, Invoke: run, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: r, Doc: collection.Doc{Params: params}}}
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
}

func installRun(deps []string) rollback.Run {
	return rollback.Run{ID: "run-L", Sealed: true, Entries: []engine.JournalEntry{
		undo(entry("run-L", "tasks[0]", "h1", 1, "rbl.install", engine.OutcomeChanged), "rbl.uninstall", map[string]any{"name": "jq", "deps": deps}),
	}}
}

func TestBuild_ARecordedListIsReplayedAsTheRunbookWouldWriteIt(t *testing.T) {
	registerInstall(t)
	plan, err := rollback.Build(rollback.Request{Target: installRun([]string{"libjq1", "libonig5"}), Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	steps := plan.Steps()
	if len(steps) != 1 || !reflect.DeepEqual(steps[0].Params["deps"], []any{"libjq1", "libonig5"}) {
		t.Fatalf("steps %+v, want the uninstall carrying the list as []any", steps)
	}

	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: ran\nhosts: h1\ntasks:\n  - name: install\n    rbl.install:\n      name: jq\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []rollback.Step{steps[0], throughJSON(t, steps[0])} {
		if err := rollback.Verify(dag, s); err != nil {
			t.Errorf("Verify: %v", err)
		}
	}
	bad := steps[0]
	bad.Params = map[string]any{"name": "jq", "deps": []any{"libjq1", 7}}
	if err := rollback.Verify(dag, bad); err == nil || !strings.Contains(err.Error(), "would never hold") {
		t.Errorf("a list holding a number: Verify = %v", err)
	}
}

func TestBuild_RefusesAListTheJournalWouldNeverHold(t *testing.T) {
	registerInstall(t)
	many := make([]string, 129)
	for i := range many {
		many[i] = "pkg"
	}
	// A list where the undo takes one piece of text is refused, whatever it holds.
	asName := installRun([]string{"libjq1"})
	asName.Entries[0].InverseParams[1] = engine.InverseParam{Key: "name", Texts: []string{"jq"}}
	if problems := refused(t, func() error {
		_, err := rollback.Build(rollback.Request{Target: asName, Devices: inventoryOf("h1")})
		return err
	}()); len(problems) != 1 || !strings.Contains(problems[0].Reason, "list the journal would never record") {
		t.Errorf("a list for name: problems %+v", problems)
	}

	for name, deps := range map[string][]string{
		"an empty list":      {},
		"too many names":     many,
		"a name with a line": {"lib\nfake"},
	} {
		run := installRun(deps)
		if len(deps) == 0 {
			run.Entries[0].InverseParams[0].Texts = []string{}
		}
		_, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
		problems := refused(t, err)
		if len(problems) != 1 || !strings.Contains(problems[0].Reason, "list the journal would never record") {
			t.Errorf("%s: problems %+v", name, problems)
		}
	}
}
