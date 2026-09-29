// Tests for building a rollback dispatch's runbook (rollback.go): every
// step held to the Runner's own copy of the runbook the undone job ran,
// before any runs.
package native

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ranRollbackFixture registers a maker whose undo is a remover, and
// returns the runbook a job ran with it.
func ranRollbackFixture(t *testing.T) *engine.DAG {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	for _, d := range []collection.Descriptor{
		{Name: "rbtest.remove", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "gone is gone"},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "path", Type: "string"}}}}},
		{Name: "rbtest.make", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rbtest.remove", Record: []string{"path"}}}},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "path", Type: "string"}}}}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: ran\ntasks:\n  - name: make it\n    rbtest.make:\n      path: /tmp/made\n"))
	if err != nil {
		t.Fatal(err)
	}
	return dag
}

func TestRollbackDAG_RunsOnlyStepsThatHoldAgainstTheRunbook(t *testing.T) {
	ran := ranRollbackFixture(t)
	step := wire.RollbackStep{Node: "tasks[0]", Emitter: "rbtest.make", Method: "rbtest.remove", Params: map[string]any{"path": "/tmp/made"}, Name: "undo tasks[0]", Source: "recorded"}
	payload := wire.DispatchPayload{RunbookID: "ran", DeviceName: "web1", Rollback: &wire.Rollback{Of: "job-1", DAGVersion: ran.Version, Steps: []wire.RollbackStep{step}}}

	dag, option, err := rollbackDAG(ran, payload)
	if err != nil {
		t.Fatalf("rollbackDAG: %v", err)
	}
	if option == nil || len(dag.Nodes) != 1 || dag.Nodes["tasks[0]"].FQCN != "rbtest.remove" || dag.Nodes["tasks[0]"].Params["path"] != "/tmp/made" {
		t.Errorf("built %+v, want the one undo step", dag.Nodes)
	}
	if _, hasTarget := dag.Nodes["tasks[0]"].Params["target"]; hasTarget {
		t.Error("a Runner's rollback task names a device; its resolver runs every task on the one dispatched")
	}

	for name, tc := range map[string]struct {
		edit func(*wire.DispatchPayload)
		want string
	}{
		"another version of the runbook": {func(p *wire.DispatchPayload) { p.Rollback.DAGVersion = "sha256:other" }, "not the version"},
		"no steps":                       {func(p *wire.DispatchPayload) { p.Rollback.Steps = nil }, "names no step"},
		"a step the runbook disowns":     {func(p *wire.DispatchPayload) { p.Rollback.Steps[0].Emitter = "rbtest.remove" }, "does not hold"},
		"a value never declared":         {func(p *wire.DispatchPayload) { p.Rollback.Steps[0].Params = map[string]any{"target": "db1"} }, "does not hold"},
	} {
		t.Run(name, func(t *testing.T) {
			p := payload
			rb := *payload.Rollback
			rb.Steps = append([]wire.RollbackStep(nil), payload.Rollback.Steps...)
			p.Rollback = &rb
			tc.edit(&p)
			if _, _, err := rollbackDAG(ran, p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("rollbackDAG = %v, want a refusal mentioning %q", err, tc.want)
			}
		})
	}
}
