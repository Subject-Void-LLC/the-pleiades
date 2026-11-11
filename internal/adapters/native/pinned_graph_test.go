// Package native: that Execute runs a dispatch only on the graph its job
// pinned (wire.DispatchPayload.DAGVersion), as it runs a rollback only on the
// version it undoes.
package native

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// pinnedRunbook runs one task, of a method that counts its runs.
const pinnedRunbook = `id: pinned
tasks:
  - name: count it
    acme.test_pinned: {}
`

// TestAdapter_Execute_RunsOnlyThePinnedGraph runs dispatches through the real
// Execute over a real runbook directory: one naming this Runner's graph of
// the runbook, by its digest under graph/1, runs its task; one naming another
// graph (another method, or the same runbook with another rollback: list,
// which DAG.Version leaves out), or a scheme this build does not know, runs
// nothing, ends failed in the job's log with the reason, and returns it; and
// one naming no graph (an older Controller's) runs as before. The version is
// never compared.
func TestAdapter_Execute_RunsOnlyThePinnedGraph(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	var ran atomic.Int32
	if err := collection.Register(collection.Descriptor{Name: "acme.test_pinned",
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			ran.Add(1)
			return collection.Result{}, nil
		},
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "reads"}},
		// A program's method runs in this process, as Execute routes one, so
		// the count is this test's to read.
		Provider: &collection.Provider{Program: "/opt/collections/acme", Digest: "sha256:00"}}); err != nil {
		t.Fatal(err)
	}
	runbooks := writeRunbook(t, "pinned", pinnedRunbook)
	dag, err := runbooks.GetDAG(context.Background(), "pinned")
	if err != nil {
		t.Fatal(err)
	}
	digest := runbook.GraphDigest(runbook.NodesOf(dag))
	// A copy alike but for a rollback: list: the same version, another graph.
	listEdited, err := writeRunbook(t, "pinned", pinnedRunbook+"    rollback:\n      - name: undo it\n        acme.test_pinned: {}\n").GetDAG(context.Background(), "pinned")
	if err != nil {
		t.Fatal(err)
	}
	if listEdited.Version != dag.Version {
		t.Fatalf("a rollback: list moved the version, so this would not test the digest alone")
	}
	otherNodes := runbook.NodesOf(dag)
	otherNodes["tasks[0]"] = runbook.Node{Method: "acme.other"}
	const refusal = "is not the graph job job-1 was prepared from"
	for _, tc := range []struct {
		name, scheme, digest, refused string
	}{
		{name: "this Runner's graph", scheme: runbook.GraphSchemeV1, digest: digest},
		{name: "another method", scheme: runbook.GraphSchemeV1, digest: runbook.GraphDigest(otherNodes), refused: refusal},
		{name: "another rollback: list", scheme: runbook.GraphSchemeV1, digest: runbook.GraphDigest(runbook.NodesOf(listEdited)), refused: refusal},
		{name: "an unknown scheme", scheme: "graph/9", digest: digest, refused: "names a graph digest scheme this Runner does not know"},
		{name: "no graph"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ran.Load()
			bus := &mockBus{}
			adapter, err := NewAdapter(bus, runbooks, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Execute(context.Background(), wire.DispatchPayload{JobID: "job-1", RunbookID: "pinned", DeviceName: "web1", DeviceHost: "10.0.0.1", GraphScheme: tc.scheme, GraphDigest: tc.digest})
			done := bus.lastJobEvent(t)
			if tc.refused == "" {
				if err != nil || ran.Load() != before+1 || done.Status == "failed" {
					t.Errorf("Execute = %v, ran %d times, ended %+v; want the task run once", err, ran.Load()-before, done)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refused) {
				t.Errorf("Execute = %v, want a refusal saying %q", err, tc.refused)
			}
			if ran.Load() != before {
				t.Errorf("the task ran %d times on another graph of the runbook", ran.Load()-before)
			}
			if done.Task != "task.completed" || done.Status != "failed" || !strings.Contains(done.EventData.Message, tc.refused) {
				t.Errorf("the job's log ended %+v, want it failed with the reason", done)
			}
			raw, _ := json.Marshal(done)
			if strings.Contains(string(raw), "acme.test_pinned") {
				t.Errorf("the refusal names the task, which never ran: %s", raw)
			}
		})
	}
}
