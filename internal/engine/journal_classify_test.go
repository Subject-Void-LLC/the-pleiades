// Tests that the run journal tells a failure the author's failed_when made
// apart from one the method made (Phase 88's cross-reference to Phase 40),
// and that under a loop failed_when and changed_when decide each item from
// that item's own result, each item journaled on its own (Phase 89). Every
// journal here is written by the real file store and read back through the
// strict reader, as pleiades rollback reads one.
package engine_test

import (
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// entriesByID indexes journal entries by node id.
func entriesByID(entries []engine.JournalEntry) map[string]engine.JournalEntry {
	out := make(map[string]engine.JournalEntry, len(entries))
	for _, e := range entries {
		out[e.NodeID] = e
	}
	return out
}

func TestJournal_AFailedWhenFailureIsNotTheMethodsFailure(t *testing.T) {
	// tasks[0]: the method succeeds and changes the device, and failed_when
	// fails it. tasks[1]: the method itself fails. Both are ignored so the
	// run goes on and both reach the journal.
	result, entries := journaledRun(t, `id: fw
hosts: web
tasks:
  - rbtest.step: {note: judged}
    register: r
    failed_when: ["result.r.code != 0"]
    ignore_errors: true
  - rbtest.step: {note: broke}
    ignore_errors: true
`, func(n int, _ *engine.Task) (engine.ActionResult, error) {
		if n == 0 {
			return engine.ActionResult{Changed: true, Stats: map[string]any{"code": 2}}, nil
		}
		return engine.ActionResult{}, errors.New("the method broke")
	})
	if result.HasErrors() {
		t.Fatalf("ignore_errors did not ignore a failed_when failure: %+v", result.Nodes)
	}
	got := entriesByID(entries)
	judged, broke := got["tasks[0]"], got["tasks[1]"]
	if judged.Outcome != engine.OutcomeFailed || judged.FailureStage != engine.FailureStageFailedWhen {
		t.Errorf("failed_when entry: outcome %q stage %q, want failed at %q", judged.Outcome, judged.FailureStage, engine.FailureStageFailedWhen)
	}
	// The method did change the device before failed_when judged it, and the
	// journal keeps that, so a rollback still undoes it.
	if !judged.ActionChanged {
		t.Error("failed_when entry lost the method's own change; a rollback must still see it")
	}
	if broke.Outcome != engine.OutcomeFailed || broke.FailureStage != engine.FailureStageAction {
		t.Errorf("method failure entry: outcome %q stage %q, want failed at %q", broke.Outcome, broke.FailureStage, engine.FailureStageAction)
	}
}

func TestJournal_AChangedWhenThatCannotBeEvaluatedHasItsOwnStage(t *testing.T) {
	// changed_when reads a field the method never returned, so evaluating it
	// fails over a run the method finished.
	_, entries := journaledRun(t, `id: cw
hosts: web
tasks:
  - rbtest.step: {note: x}
    register: r
    changed_when: ["result.r.missing.deeper == 1"]
    ignore_errors: true
`, func(int, *engine.Task) (engine.ActionResult, error) {
		return engine.ActionResult{Stats: map[string]any{"code": 0}}, nil
	})
	e := entriesByID(entries)["tasks[0]"]
	if e.Outcome != engine.OutcomeFailed || e.FailureStage != engine.FailureStageChangedWhen {
		t.Errorf("entry: outcome %q stage %q, want failed at %q", e.Outcome, e.FailureStage, engine.FailureStageChangedWhen)
	}
}

func TestUntil_RetriesAFailedWhenFailure(t *testing.T) {
	// The first try's result fails failed_when; until tries again, as it does
	// for a failure of the method, and the second try holds.
	result, entries := journaledRun(t, `id: ufw
hosts: web
tasks:
  - rbtest.step: {note: x}
    register: r
    failed_when: ["result.r.code != 0"]
    until: ["result.r.code == 0"]
    retries: 2
    delay: 0
`, func(n int, _ *engine.Task) (engine.ActionResult, error) {
		// The first try returns 1, the second 0.
		return engine.ActionResult{Stats: map[string]any{"code": 1 - n}}, nil
	})
	if result.HasErrors() {
		t.Fatalf("until did not retry a failed_when failure: %+v", result.Nodes)
	}
	got := entriesByID(entries)
	if e := got["tasks[0].attempt[0]"]; e.Outcome != engine.OutcomeFailed || e.FailureStage != engine.FailureStageFailedWhen {
		t.Errorf("attempt 0: outcome %q stage %q, want failed at failed_when", e.Outcome, e.FailureStage)
	}
	if e := got["tasks[0].attempt[1]"]; e.Outcome != engine.OutcomeRan {
		t.Errorf("attempt 1: outcome %q, want ran", e.Outcome)
	}
}

func TestLoop_FailedWhenAndChangedWhenDecideEachItem(t *testing.T) {
	// Each item's method returns its own code. failed_when fails the item
	// whose code is 2, and changed_when marks changed only the item whose
	// code is 1: each reads that item's own result, never the list.
	result, entries := journaledRun(t, `id: lfw
hosts: web
tasks:
  - rbtest.step:
      note: "{{ item }}"
    loop: [0, 1, 2]
    register: r
    failed_when: ["result.r.code == 2"]
    changed_when: ["result.r.code == 1"]
`, func(n int, _ *engine.Task) (engine.ActionResult, error) {
		return engine.ActionResult{Stats: map[string]any{"code": n}}, nil
	})
	if !result.HasErrors() {
		t.Fatal("the looped task did not fail although failed_when held for one item")
	}
	got := entriesByID(entries)
	want := map[string]struct {
		outcome engine.Outcome
		stage   engine.FailureStage
	}{
		"tasks[0].item[0]": {engine.OutcomeRan, engine.FailureStageNone},
		"tasks[0].item[1]": {engine.OutcomeChanged, engine.FailureStageNone},
		"tasks[0].item[2]": {engine.OutcomeFailed, engine.FailureStageFailedWhen},
	}
	for id, w := range want {
		e, ok := got[id]
		if !ok {
			t.Errorf("no journal entry for %s; entries %v", id, outcomesByID(entries))
			continue
		}
		if e.Outcome != w.outcome || e.FailureStage != w.stage {
			t.Errorf("%s: outcome %q stage %q, want %q %q", id, e.Outcome, e.FailureStage, w.outcome, w.stage)
		}
	}
	if len(got) != len(want) {
		t.Errorf("journal holds %d entries, want one per item: %v", len(got), outcomesByID(entries))
	}
}

func TestLoop_FailedWhenUnderALoopRegistersEachItemsOwnVerdict(t *testing.T) {
	// The register of a looped task holds each item's own changed and failed,
	// as failed_when and changed_when decided them, and the whole's.
	_, result, wf := runStepper(t, collection.ModeExecute, `id: lreg
hosts: web
tasks:
  - rbtest.step:
      note: "{{ item }}"
    loop: [a, b]
    register: r
    failed_when: ["result.r.said == 'b'"]
    changed_when: ["result.r.said == 'a'"]
    ignore_errors: true
`, echo)
	if result.HasErrors() {
		t.Fatalf("ignore_errors over the loop did not hold: %+v", result.Nodes)
	}
	tree, err := wf.Read()
	if err != nil {
		t.Fatal(err)
	}
	agg := tree["r"].(map[string]any)["a"].(map[string]any)
	rows := agg["results"].([]any)
	if len(rows) != 2 {
		t.Fatalf("register rows %v, want two", rows)
	}
	a, b := rows[0].(map[string]any), rows[1].(map[string]any)
	if a["item"] != "a" || a["changed"] != true || a["failed"] != false {
		t.Errorf("row a %v, want changed and not failed", a)
	}
	if b["item"] != "b" || b["changed"] != false || b["failed"] != true || b["msg"] == nil {
		t.Errorf("row b %v, want failed with a msg and not changed", b)
	}
	if agg["changed"] != true || agg["failed"] != true {
		t.Errorf("aggregate %v, want changed and failed for the whole", agg)
	}
}
