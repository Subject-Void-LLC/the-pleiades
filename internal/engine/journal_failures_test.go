// Tests that the run journal tells the truth about a failure the run went
// past: an ignored failure and a retried one are recorded as failures, with
// the id of the run they belong to, and a loop of items is journaled item by
// item whatever the older with_items spelling.
package engine_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// journaledRun runs runbook with fn behind rbtest.step and a real journal
// file store, and returns the entries it read back through the strict reader.
func journaledRun(t *testing.T, runbook string, fn func(int, *engine.Task) (engine.ActionResult, error)) (engine.RunResult, []engine.JournalEntry) {
	t.Helper()
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &stepper{fn: fn, builtin: engine.NewBuiltinActionExecutor()}
	x := engine.NewExecutor(oneDevice(), s, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()), engine.WithJournal(store))
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := store.Seal(result.RunID, result.Nodes[len(result.Nodes)-1].FinishedAt); err != nil {
		t.Fatal(err)
	}
	run, err := journal.ReadRun(root, result.RunID)
	if err != nil {
		t.Fatalf("the strict reader refused the journal: %v", err)
	}
	return result, run.Entries
}

func outcomesByID(entries []engine.JournalEntry) map[string]engine.Outcome {
	out := map[string]engine.Outcome{}
	for _, e := range entries {
		out[e.NodeID] = e.Outcome
	}
	return out
}

func TestJournal_AnIgnoredAndARetriedFailureAreRecordedAsFailures(t *testing.T) {
	// Ignored: the run goes on, the entry says failed.
	result, entries := journaledRun(t, "id: ig\nhosts: web\ntasks:\n  - rbtest.step: {note: first}\n    ignore_errors: true\n  - rbtest.step: {note: second}\n",
		func(n int, _ *engine.Task) (engine.ActionResult, error) {
			if n == 0 {
				return engine.ActionResult{}, errors.New("broke")
			}
			return engine.ActionResult{}, nil
		})
	if result.HasErrors() {
		t.Fatalf("an ignored failure failed the run: %+v", result.Nodes)
	}
	got := outcomesByID(entries)
	if got["tasks[0]"] != engine.OutcomeFailed || got["tasks[1]"] == engine.OutcomeFailed {
		t.Errorf("ignored: outcomes %v", got)
	}

	// Retried: every try has its own entry, the failed try is recorded failed,
	// the last (which held) is not.
	result, entries = journaledRun(t, "id: rt\nhosts: web\ntasks:\n  - rbtest.step: {note: x}\n    register: r\n    until: [\"result.r.ok == true\"]\n    retries: 3\n    delay: 0\n",
		func(n int, _ *engine.Task) (engine.ActionResult, error) {
			if n == 0 {
				return engine.ActionResult{}, errors.New("not yet")
			}
			return engine.ActionResult{Stats: map[string]any{"ok": true}}, nil
		})
	if result.HasErrors() {
		t.Fatalf("a retried failure failed the run: %+v", result.Nodes)
	}
	got = outcomesByID(entries)
	if got["tasks[0].attempt[0]"] != engine.OutcomeFailed || got["tasks[0].attempt[1]"] == engine.OutcomeFailed || len(got) != 2 {
		t.Errorf("retried: outcomes %v", got)
	}
}

func TestWithItems_RunsLikeLoopAndComposesWithIgnoreErrors(t *testing.T) {
	s, result, _ := runStepper(t, collection.ModeExecute, "id: wi\nhosts: web\ntasks:\n  - rbtest.step:\n      note: \"{{ item }}\"\n    with_items: [a, b]\n", echo)
	if result.HasErrors() || !slices.Equal(s.calls, []string{"a", "b"}) {
		t.Fatalf("with_items: calls %v, %+v", s.calls, result.Nodes)
	}
	// One item fails, the task is ignored, the others still ran and the run
	// carries on to the task after.
	s, result, _ = runStepper(t, collection.ModeExecute, "id: li\nhosts: web\ntasks:\n  - rbtest.step:\n      note: \"{{ item }}\"\n    loop: [a, b, c]\n    ignore_errors: true\n  - rbtest.step: {note: after}\n",
		func(n int, task *engine.Task) (engine.ActionResult, error) {
			if n == 1 {
				return engine.ActionResult{}, errors.New("item b broke")
			}
			return echo(n, task)
		})
	if result.HasErrors() || !slices.Equal(s.calls, []string{"a", "b", "c", "after"}) {
		t.Fatalf("ignore_errors over a loop: calls %v, %+v", s.calls, result.Nodes)
	}
	ignored := 0
	for _, n := range result.Nodes {
		if n.Ignored {
			ignored++
		}
	}
	if ignored != 1 {
		t.Errorf("%d results marked ignored, want only item b", ignored)
	}
}
