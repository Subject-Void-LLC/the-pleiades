// Tests for a Runner's re-check of a rollback dispatch's declarations
// (rollback.go over internal/rollback's skew.go): a step whose undo was
// recorded under other declarations than this Runner's is refused unless
// the dispatch carries the operator's acceptance, and is logged naming
// what was accepted when it runs.
package native

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// skewFixture registers file.rbmake, whose undo is file.rbremove, in the
// first-party group "file", and returns the runbook a job ran with it, this
// Runner's identity of the group and the digest of the undo.
func skewFixture(t *testing.T) (*engine.DAG, wire.CollectionIdentity, string) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	spec := sdk.InverseSpec{FQCN: "file.rbremove", Record: []string{"path"}}
	for _, d := range []collection.Descriptor{
		{Name: "file.rbremove", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "gone is gone"},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "path", Type: "string", Required: true}}}}},
		{Name: "file.rbmake", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec}},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "path", Type: "string"}}}}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	id, err := collection.BuiltinIdentity("file.rbmake", buildinfo.Version())
	if err != nil {
		t.Fatal(err)
	}
	target, _ := collection.ManifestOf("file.rbremove")
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: ran\ntasks:\n  - name: make it\n    file.rbmake:\n      path: /tmp/made\n"))
	if err != nil {
		t.Fatal(err)
	}
	return dag, id, collection.UndoDigest(spec, fragment.ByNameIn(target))
}

func TestRollbackDAG_AForeignUndoRunsOnlyWhenAcceptedAndIsLogged(t *testing.T) {
	ran, id, undo := skewFixture(t)
	older := id
	older.Version, older.Manifests = "0.3.9", wire.ManifestDigestPrefix+strings.Repeat("a", 64)
	recorded := wire.ManifestDigestPrefix + strings.Repeat("b", 64)
	step := wire.RollbackStep{Node: "tasks[0]", Emitter: "file.rbmake", Method: "file.rbremove", Params: map[string]any{"path": "/tmp/made"},
		Name: "undo tasks[0]", Source: "recorded", Collection: older, UndoDigest: recorded}
	payload := wire.DispatchPayload{JobID: "job-2", RunbookID: "ran", DeviceName: "web1",
		Rollback: &wire.Rollback{Of: "job-1", DAGVersion: ran.Version, Steps: []wire.RollbackStep{step}}}

	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	if _, _, err := rollbackDAG(logger, ran, payload); err == nil || !strings.Contains(err.Error(), "allow_skew") || !strings.Contains(err.Error(), undo) {
		t.Fatalf("a foreign undo, not accepted: %v, want a refusal naming allow_skew and this Runner's undo digest", err)
	}

	payload.Rollback.Steps[0].SkewAccepted = true
	if _, _, err := rollbackDAG(logger, ran, payload); err != nil {
		t.Fatalf("a foreign undo the rollback accepted by name: %v", err)
	}
	for _, want := range []string{"under other declarations", "job=job-2", "rollback_of=job-1", "node=tasks[0]", recorded, undo} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the log does not name %s:\n%s", want, logged.String())
		}
	}

	// The Runner's own declarations: nothing to accept, nothing logged.
	logged.Reset()
	payload.Rollback.Steps[0].Collection, payload.Rollback.Steps[0].UndoDigest = id, undo
	if _, _, err := rollbackDAG(logger, ran, payload); err != nil || logged.Len() != 0 {
		t.Errorf("a step under this Runner's declarations: %v, logged %q", err, logged.String())
	}
}
