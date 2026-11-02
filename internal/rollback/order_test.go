// Tests for ordering runs without a clock (order.go): the Crawl tier's run
// ticks and the Walk tier's lease revisions decide which run came later on a
// device, whatever the journals' clock times say, and clock times decide only
// for a journal from before either existed, which the plan names.
package rollback_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// ticked returns run with every entry carrying start tick start, and end
// tick end on its seal.
func ticked(run rollback.Run, start, end uint64) rollback.Run {
	out := run
	out.Entries = slices.Clone(run.Entries)
	for i := range out.Entries {
		out.Entries[i].RunTick = start
	}
	out.EndTick = end
	return out
}

// revised returns run with every entry carrying lease revision rev.
func revised(run rollback.Run, rev uint64) rollback.Run {
	out := run
	out.Entries = slices.Clone(run.Entries)
	for i := range out.Entries {
		out.Entries[i].LeaseRevision = rev
	}
	return out
}

// shifted returns run with every entry's times moved by d, as a clock set
// differently when it ran would have written them.
func shifted(run rollback.Run, d time.Duration) rollback.Run {
	out := run
	out.Entries = slices.Clone(run.Entries)
	for i := range out.Entries {
		out.Entries[i].StartedAt = out.Entries[i].StartedAt.Add(d)
		out.Entries[i].FinishedAt = out.Entries[i].FinishedAt.Add(d)
	}
	return out
}

// stopsH1 is a run that stopped the VM on h1, at seq (which orders its
// times), with an undo.
func stopsH1(id string, seq int) rollback.Run {
	return rollback.Run{ID: id, Sealed: true, Entries: []engine.JournalEntry{
		undo(entry(id, "tasks[0]", "h1", seq, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "bsd"}),
	}}
}

// TestOrder_TheCrawlTierOrdersRunsByTickNotClock: a run that ticked after the
// one undone is later even when the clock was stepped back between them, and
// one that ended before it began is not, even when its clock ran ahead.
func TestOrder_TheCrawlTierOrdersRunsByTickNotClock(t *testing.T) {
	registerShapes(t)
	target := ticked(createRun(), 3, 4)
	// Ticked after, written under a clock stepped back a day.
	stepped := ticked(shifted(stopsH1("run-B", 20), -24*time.Hour), 5, 6)
	_, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{stepped}, Devices: inventoryOf("h1")})
	problems := refused(t, err)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "run run-B changed") || strings.Contains(problems[0].Reason, "clock") {
		t.Fatalf("problems %+v, want run-B refused by its tick, with no word of clocks", problems)
	}
	// The control: the same journals with no ticks are ordered by clock, and
	// the stepped clock hides run-B. That is the hole the ticks close.
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{shifted(stopsH1("run-B", 20), -24*time.Hour)}, Devices: inventoryOf("h1")}); err != nil {
		t.Fatalf("by clock, run-B read as later after all (%v): the control does not reproduce the hole", err)
	}

	// Ended before the target began, though its clock ran a day ahead.
	ahead := ticked(shifted(stopsH1("run-E", 20), 24*time.Hour), 1, 2)
	plan, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{ahead}, Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("an earlier run with a fast clock refused the rollback: %v", err)
	}
	if len(plan.OrderedByClock) != 0 {
		t.Errorf("OrderedByClock = %v for two ticked runs", plan.OrderedByClock)
	}

	// Began before the target and ended after it began: it overlapped, so it
	// may have changed h1 after the target did.
	overlapping := ticked(stopsH1("run-O", 20), 2, 5)
	if _, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{overlapping}, Devices: inventoryOf("h1")}); err == nil {
		t.Error("a run that overlapped the target did not count as later")
	}
}

// TestOrder_ARunThatNeverRecordedItsEndCountsAsLater: a run with a start tick
// and no end tick (killed, or still running) may have changed the device at
// any time, so it is refused, says why, and --despite-run accepts it.
func TestOrder_ARunThatNeverRecordedItsEndCountsAsLater(t *testing.T) {
	registerShapes(t)
	target := ticked(createRun(), 3, 4)
	killed := ticked(stopsH1("run-K", 20), 1, 0)
	killed.Sealed = false
	_, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{killed}, Devices: inventoryOf("h1")})
	problems := refused(t, err)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "never recorded its end") || problems[0].Accept != "--despite-run run-K" {
		t.Fatalf("problems %+v, want run-K refused as possibly later, with its flag", problems)
	}
	if _, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{killed}, Devices: inventoryOf("h1"), DespiteRuns: map[string]bool{"run-K": true}}); err != nil {
		t.Errorf("--despite-run run-K did not accept it: %v", err)
	}
}

// TestOrder_AJournalFromBeforeTheCounterIsOrderedByClockAndNamed: a run with
// no tick against a ticked target is ordered by clock, and the plan names it.
func TestOrder_AJournalFromBeforeTheCounterIsOrderedByClockAndNamed(t *testing.T) {
	registerShapes(t)
	target := ticked(createRun(), 3, 4)
	legacy := shifted(stopsH1("run-L", 20), -24*time.Hour) // ended before, by clock
	plan, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{legacy}, Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !slices.Equal(plan.OrderedByClock, []string{"run-L"}) {
		t.Errorf("OrderedByClock = %v, want run-L", plan.OrderedByClock)
	}
	laterLegacy := stopsH1("run-L", 20) // later, by clock
	_, err = rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{laterLegacy}, Devices: inventoryOf("h1")})
	if problems := refused(t, err); !strings.Contains(problems[0].Reason, "decided by the journals' clock times") || !strings.Contains(problems[0].Reason, "shared device lease") {
		t.Errorf("a refusal decided by clock does not say so: %q", problems[0].Reason)
	}
	// A run that only read the device changed nothing the order could
	// decide about, so it is not named either.
	reader := rollback.Run{ID: "run-R", Sealed: true, Entries: []engine.JournalEntry{entry("run-R", "tasks[0]", "h1", 20, "rb.read", engine.OutcomeRan)}}
	plan, err = rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{reader}, Devices: inventoryOf("h1")})
	if err != nil || len(plan.OrderedByClock) != 0 {
		t.Errorf("a run that only read h1: plan %v, err %v; want no clock ordering named", plan.OrderedByClock, err)
	}
	// A run on another device needs no order at all, so it is not named.
	elsewhere := rollback.Run{ID: "run-X", Sealed: true, Entries: []engine.JournalEntry{entry("run-X", "tasks[0]", "h2", 20, "rb.read", engine.OutcomeRan)}}
	plan, err = rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{elsewhere}, Devices: inventoryOf("h1", "h2")})
	if err != nil || len(plan.OrderedByClock) != 0 {
		t.Errorf("a run on another device: plan %v, err %v; want no clock ordering named", plan.OrderedByClock, err)
	}
}

// TestOrder_TheWalkTierOrdersJobsByLeaseRevisionNotClock: on a device, a job
// whose lease revision is above the target's is later even when its Runner's
// clock ran behind, one below is not even when its clock ran ahead, and a
// target redelivered under a newer lease is ordered from its first attempt.
func TestOrder_TheWalkTierOrdersJobsByLeaseRevisionNotClock(t *testing.T) {
	registerShapes(t)
	target := revised(createRun(), 10)
	skewed := revised(shifted(stopsH1("job-B", 20), -24*time.Hour), 20)
	if problems := refused(t, func() error {
		_, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{skewed}, Devices: inventoryOf("h1")})
		return err
	}()); len(problems) != 1 || !strings.Contains(problems[0].Reason, "job-B") {
		t.Fatalf("problems %+v, want job-B, behind on the clock but later by lease", problems)
	}
	ahead := revised(shifted(stopsH1("job-E", 20), 24*time.Hour), 5)
	if _, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{ahead}, Devices: inventoryOf("h1")}); err != nil {
		t.Errorf("an earlier job with a fast clock refused the rollback: %v", err)
	}
	// The target ran twice, under leases 10 and 30; job-B, under 20, came
	// after its first attempt changed h1.
	redelivered := createRun()
	second := revised(createRun(), 30)
	for i := range second.Entries {
		second.Entries[i].Attempt = 2
	}
	redelivered = revised(redelivered, 10)
	redelivered.Entries = append(redelivered.Entries, second.Entries...)
	if _, err := rollback.Build(rollback.Request{Target: redelivered, Others: []rollback.Run{revised(stopsH1("job-B", 20), 20)}, Devices: inventoryOf("h1")}); err == nil {
		t.Error("a job between the target's two attempts did not count as later")
	}
}

// TestOrder_TheLatestUndoAttemptIsFoundWithoutAClock: of two rollbacks of a
// later run, the one that ticked last decides whether that run is undone,
// whatever their clocks say. Here the last one failed, so the later run still
// counts.
func TestOrder_TheLatestUndoAttemptIsFoundWithoutAClock(t *testing.T) {
	registerShapes(t)
	target := ticked(createRun(), 3, 4)
	later := ticked(stopsH1("run-B", 20), 5, 6)
	rollbackOfB := func(id string, seq int, outcome engine.Outcome, start, end uint64, clock time.Duration) rollback.Run {
		e := entry(id, "tasks[0]", "h1", seq, "rb.start", outcome)
		e.RollbackOf, e.UndoesNode = "run-B", "tasks[0]"
		if outcome == engine.OutcomeFailed {
			e.FailureStage = engine.FailureStageAction
		}
		return ticked(shifted(rollback.Run{ID: id, Sealed: true, Entries: []engine.JournalEntry{e}}, clock), start, end)
	}
	succeededFirst := rollbackOfB("run-RB1", 30, engine.OutcomeChanged, 7, 8, 24*time.Hour) // clock ahead
	failedLast := rollbackOfB("run-RB2", 31, engine.OutcomeFailed, 9, 10, 0)
	if _, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{later, succeededFirst, failedLast}, Devices: inventoryOf("h1")}); err == nil {
		t.Error("run-B, whose last rollback (by tick) failed, did not count as a later change")
	}
	succeededLast := rollbackOfB("run-RB2", 31, engine.OutcomeChanged, 9, 10, -24*time.Hour) // clock behind
	failedFirst := rollbackOfB("run-RB1", 30, engine.OutcomeFailed, 7, 8, 24*time.Hour)
	if _, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{later, failedFirst, succeededLast}, Devices: inventoryOf("h1")}); err != nil {
		t.Errorf("run-B, whose last rollback (by tick) succeeded, still refused: %v", err)
	}
}
