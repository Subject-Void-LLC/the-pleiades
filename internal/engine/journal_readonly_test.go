// Tests that the journal records whether a call only read, asked of the
// parameters it ran with, through the real Executor and the real collection
// bridge: a failed read records it, so a rollback can tell it from a failed
// change (internal/rollback's changesNothing), and nothing else does.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerReadOnlyFixtures registers enginetest.cmd, whose ReadOnlyCall
// answers true for a show, as net.ios.command's does, and which fails every
// call it is handed, as a device refusing the line or a login that never
// finished does; and enginetest.read, read-only as a whole.
func registerReadOnlyFixtures(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	fail := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, errors.New("% Invalid input detected at '^' marker")
	}
	where := collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceNone}
	params := collection.Doc{Params: []collection.Param{{Name: "command", Type: "string"}}}
	for _, d := range []collection.Descriptor{
		{Name: "enginetest.cmd", Invoke: fail, Manifest: collection.Manifest{Status: collection.StatusImplemented, ExecutionContext: where, Doc: params,
			Reversibility: collection.Reversibility{Notes: "an arbitrary command"}},
			ReadOnlyCall: func(p map[string]any) bool {
				line, _ := p["command"].(string)
				return strings.HasPrefix(line, "show ") && !collection.IsTemplate(line)
			}},
		{Name: "enginetest.read", Invoke: fail, Manifest: collection.Manifest{Status: collection.StatusImplemented, ExecutionContext: where, Doc: params,
			Reversibility: collection.Reversibility{Notes: "reads", ReadOnly: true}}},
		// A method that declares it only reads and reports a change anyway,
		// which the engine fails (holdReadOnly).
		{Name: "enginetest.liar", Manifest: collection.Manifest{Status: collection.StatusImplemented, ExecutionContext: where, Doc: params,
			Reversibility: collection.Reversibility{Notes: "reads", ReadOnly: true}},
			Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
				return collection.Result{Changed: true}, nil
			}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
}

// TestJournalRecordsAFailedReadAsOnlyReading: a failed call whose method
// answers that it only reads, of its rendered parameters, is journaled so;
// a templated show is asked once rendered, never as its source; a failed
// call that does not read, and a failed engine action, are not.
func TestJournalRecordsAFailedReadAsOnlyReading(t *testing.T) {
	registerReadOnlyFixtures(t)
	for _, tc := range []struct {
		name, task string
		want       bool
	}{
		{"a show", "enginetest.cmd:\n      command: show version", true},
		{"a show rendered from a variable", "enginetest.cmd:\n      command: \"show {{ vars.what }}\"", true},
		{"a command rendered to a show from a variable", "enginetest.cmd:\n      command: \"{{ vars.verb }} version\"", true},
		{"a method read-only as a whole", "enginetest.read:\n      command: anything", true},
		{"a command that changes", "enginetest.cmd:\n      command: clear counters", false},
		{"a command rendered to a change", "enginetest.cmd:\n      command: \"{{ vars.change }}\"", false},
		{"an engine action", "noop:\n      msg: x", false},
		// It declared a read and reported a change: it did not only read.
		{"a declared read that reported a change", "enginetest.liar:\n      command: show version", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &journalSink{}
			dag := yamlRunbook(t, "id: reads\ntasks:\n  - name: t\n    "+tc.task+"\n")
			actions := engine.NewCollectionActionExecutor(failingActionExecutor{}, engine.NewDeviceRunbookContext)
			x := engine.NewExecutor(mapResolver{}, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
				engine.WithVariables(map[string]any{"what": "version", "verb": "show", "change": "clear counters"}), engine.WithRenderer(render.New()), engine.WithJournal(sink))
			if _, err := x.Run(context.Background(), dag); err != nil {
				t.Fatalf("Run: %v", err)
			}
			entries := sink.flat()
			if len(entries) != 1 || entries[0].Outcome != engine.OutcomeFailed || entries[0].FailureStage != engine.FailureStageAction {
				t.Fatalf("entries %+v, want one failed action", entries)
			}
			if entries[0].ReadOnlyCall != tc.want {
				t.Errorf("ReadOnlyCall = %v, want %v", entries[0].ReadOnlyCall, tc.want)
			}
		})
	}
}
