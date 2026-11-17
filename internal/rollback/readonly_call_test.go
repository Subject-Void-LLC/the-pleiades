// Tests that a failed call the journal says only read (engine.JournalEntry.
// ReadOnlyCall, net.ios.command's show) changed nothing, in the run being
// undone and in a later run on the same device, while a failed call that
// does not only read still may have changed it (FAILURE_PATTERNS 1269).
package rollback_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// failedCall is a call of fqcn on h1 that failed during its action, which
// the journal says only read when readOnly.
func failedCall(run, node string, seq int, fqcn string, readOnly bool) engine.JournalEntry {
	e := entry(run, node, "h1", seq, fqcn, engine.OutcomeFailed)
	e.FailureStage, e.ReadOnlyCall = engine.FailureStageAction, readOnly
	return e
}

// TestBuild_AFailedReadOnlyCallChangedNothing: in the run undone, a failed
// show is no unknown effect, and a failed command is; beneath a later run, a
// run whose only failure was a show (refused by the device, or never logged
// in) is not one that changed the device, and one with a failed command is,
// named with --despite-run.
func TestBuild_AFailedReadOnlyCallChangedNothing(t *testing.T) {
	registerShapes(t)
	run := createRun()
	run.Entries = append(run.Entries, failedCall("run-A", "tasks[3]", 4, "rb.shell", true), failedCall("run-A", "tasks[4]", 5, "rb.shell", false))
	plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Unknown) != 1 || plan.Unknown[0].Node != "tasks[4]" {
		t.Errorf("unknown %+v, want only the failed command, not the failed read", plan.Unknown)
	}

	refusedShow := rollback.Run{ID: "run-B", Sealed: true, Entries: []engine.JournalEntry{failedCall("run-B", "tasks[0]", 20, "rb.shell", true)}}
	neverLoggedIn := rollback.Run{ID: "run-C", Sealed: true, Entries: []engine.JournalEntry{failedCall("run-C", "tasks[0]", 30, "rb.shell", true)}}
	failedChange := rollback.Run{ID: "run-D", Sealed: true, Entries: []engine.JournalEntry{failedCall("run-D", "tasks[0]", 40, "rb.shell", false)}}
	req := rollback.Request{Target: createRun(), Others: []rollback.Run{refusedShow, neverLoggedIn, failedChange}, Devices: inventoryOf("h1")}
	problems := refused(t, func() error { _, err := rollback.Build(req); return err }())
	if len(problems) != 1 || problems[0].Accept != "--despite-run run-D" || !strings.Contains(strings.Join(problems[0].UnknownEffects, ","), "rb.shell") {
		t.Fatalf("problems %+v, want only run-D named, its failed command an unknown effect", problems)
	}
	req.DespiteRuns = map[string]bool{"run-D": true}
	if _, err := rollback.Build(req); err != nil {
		t.Errorf("with run-D named, Build = %v", err)
	}
}

// TestBuild_AReadOnlyAnswerFromAProgramIsNotTaken: an entry naming an
// external Collection program is never taken at its word that the call only
// read, since the engine records no such answer for one.
func TestBuild_AReadOnlyAnswerFromAProgramIsNotTaken(t *testing.T) {
	registerShapes(t)
	run := createRun()
	claimed := failedCall("run-A", "tasks[3]", 4, "rb.shell", true)
	claimed.ProviderProgram = "acme"
	run.Entries = append(run.Entries, claimed)
	plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
	if err != nil || len(plan.Unknown) != 1 || plan.Unknown[0].Node != "tasks[3]" {
		t.Errorf("Build = %v, unknown %+v; want the program's failed call unknown", err, plan.Unknown)
	}
}

// TestBuild_AChangeIsUndoneWhateverItsReadOnlyAnswer: an entry that changed
// the device is planned as a change, whatever else it says.
func TestBuild_AChangeIsUndoneWhateverItsReadOnlyAnswer(t *testing.T) {
	registerShapes(t)
	run := createRun()
	run.Entries[0].ReadOnlyCall = true
	plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
	if err != nil || len(plan.Steps()) != 2 {
		t.Errorf("Build = %v, steps %+v; want both changes undone", err, plan.Steps())
	}
}
