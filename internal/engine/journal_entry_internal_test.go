// These tests reach the projection's unexported parts: the two closed-set
// checks that make it fail closed, and the keyword mapping that feeds
// one of them.
//
// They are the negative controls for journal_entry_test.go's end-to-end
// coverage. That file proves the projection records the right thing for
// every shape the executor produces today; this one proves it REFUSES a
// shape it does not recognize, which is the half no run can exercise
// because no run can produce a tenth failure site until somebody writes
// it.
//
// FuzzProjectLevel, which generalizes both files from examples into a
// property, lives in journal_fuzz_test.go beside them.
package engine

import (
	"fmt"
	"strings"
	"testing"
)

// buildInternalDAG compiles payload through the real Builder, so these
// tests hold a genuinely compiled graph rather than a hand-built one.
func buildInternalDAG(t *testing.T, payload string) *DAG {
	t.Helper()
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}
	dag, err := NewBuilder(eval).Build([]byte(payload))
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}
	return dag
}

// TestValidFailureStageAcceptsEveryDeclaredStage pins the ten stages
// against the check, and pins the two shapes that must be refused: the
// zero value, which is a failure site that never tagged itself, and a
// constant nobody added to the check.
func TestValidFailureStageAcceptsEveryDeclaredStage(t *testing.T) {
	declared := []FailureStage{
		FailureStageWorkflowRead,
		FailureStageConditionEval,
		FailureStageSecretMask,
		FailureStageRender,
		FailureStageResolveTarget,
		FailureStageLockAll,
		FailureStageLockDevice,
		FailureStageAction,
		FailureStageRegisterMask,
		FailureStageRecord,
	}
	if len(declared) != 10 {
		t.Fatalf("this table lists %d stages; journal.go declares ten, one per failure call site", len(declared))
	}
	for _, stage := range declared {
		if !validFailureStage(stage) {
			t.Errorf("validFailureStage(%q) = false, want true: every declared stage must project", stage)
		}
	}
	if validFailureStage(FailureStageNone) {
		t.Error("validFailureStage(FailureStageNone) = true; a failed result with no stage is the hole this refuses")
	}
	if validFailureStage(FailureStage("stage_a_later_phase_added")) {
		t.Error("validFailureStage accepted a stage it does not enumerate; adding a tenth site must not project as a zero value")
	}
}

// TestValidSkipKindAcceptsEveryDeclaredKind is the same pinning for the
// four reasons a node may be recorded as skipped.
func TestValidSkipKindAcceptsEveryDeclaredKind(t *testing.T) {
	for _, kind := range []SkipKind{SkipKindWhen, SkipKindWhenOr, SkipKindWhenCEL, SkipKindLifecycle} {
		if !validSkipKind(kind) {
			t.Errorf("validSkipKind(%q) = false, want true", kind)
		}
	}
	if validSkipKind(SkipKindNone) {
		t.Error("validSkipKind(SkipKindNone) = true; a skipped result with no kind must be refused")
	}
	if validSkipKind(SkipKind("unless")) {
		t.Error("validSkipKind accepted a kind it does not enumerate")
	}
}

// TestSkipKindForMapsEveryConditionKeyword compiles each of the three
// condition keywords through the real Conditional.Compile and asserts the
// mapping, so the journal's vocabulary is pinned to ConditionProgram's own
// keyword literals rather than to a second copy of them.
func TestSkipKindForMapsEveryConditionKeyword(t *testing.T) {
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}

	cases := []struct {
		cond Conditional
		want SkipKind
	}{
		{Conditional{When: StringList{"true"}}, SkipKindWhen},
		{Conditional{WhenOr: StringList{"true"}}, SkipKindWhenOr},
		{Conditional{WhenCEL: "true"}, SkipKindWhenCEL},
	}
	for _, tc := range cases {
		cp, err := tc.cond.Compile(eval)
		if err != nil {
			t.Fatalf("compiling %+v: %v", tc.cond, err)
		}
		if got := skipKindFor(cp); got != tc.want {
			t.Errorf("skipKindFor(%q) = %q, want %q", cp.keyword, got, tc.want)
		}
	}

	// A fourth keyword maps to no kind, which is what makes projectResult
	// refuse the entry rather than mislabel it as a plain when.
	if got := skipKindFor(&ConditionProgram{keyword: "unless"}); got != SkipKindNone {
		t.Errorf("skipKindFor on an unknown keyword = %q, want SkipKindNone", got)
	}
}

// newProjectionRun builds the minimal run value the projection needs: a
// compiled DAG and a run id. Nothing here executes anything, because the
// shapes under test are ones no execution can produce yet.
func newProjectionRun(t *testing.T) *run {
	t.Helper()
	return &run{
		dag:   buildInternalDAG(t, `{"id": "fail-closed", "tasks": [{"name": "a", "fqcn": "noop"}]}`),
		runID: "run-under-test",
	}
}

// TestProjectResultRefusesAnUntaggedFailure is the fail-closed control
// for a tenth failure construction site: a result carrying an error and
// no stage produces no entry and an error naming the node, never an entry
// recording a failure at stage "".
func TestProjectResultRefusesAnUntaggedFailure(t *testing.T) {
	r := newProjectionRun(t)

	untagged := NodeResult{NodeID: "tasks[0]", Device: "dev-1"}
	untagged.Err = errTestUntagged

	if _, err := r.projectResult(untagged); err == nil {
		t.Fatal("projectResult accepted a failed result with no stage; a zero-valued stage would be read as fact")
	} else if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("the refusal reads %q, want it to name the node an operator has to go look at", err)
	}

	// The positive half: the same result, tagged, projects cleanly. A
	// check that refused everything would pass the assertion above and be
	// useless.
	tagged := untagged
	tagged.fail(FailureStageAction, errTestUntagged)
	entry, err := r.projectResult(tagged)
	if err != nil {
		t.Fatalf("projectResult refused a properly tagged failure: %v", err)
	}
	if entry.Outcome != OutcomeFailed || entry.FailureStage != FailureStageAction {
		t.Errorf("projected outcome %q stage %q, want a failure at the action stage", entry.Outcome, entry.FailureStage)
	}
}

// TestProjectResultRefusesAnUntaggedSkip is the same control for a third
// skip site, or for a condition keyword skipKindFor does not know.
func TestProjectResultRefusesAnUntaggedSkip(t *testing.T) {
	r := newProjectionRun(t)

	if _, err := r.projectResult(NodeResult{NodeID: "tasks[0]", Skipped: true}); err == nil {
		t.Fatal("projectResult accepted a skipped result with no kind")
	}

	entry, err := r.projectResult(NodeResult{NodeID: "tasks[0]", Skipped: true, skipKind: SkipKindLifecycle})
	if err != nil {
		t.Fatalf("projectResult refused a properly tagged skip: %v", err)
	}
	if entry.Outcome != OutcomeSkipped || entry.SkipKind != SkipKindLifecycle {
		t.Errorf("projected outcome %q kind %q, want a lifecycle skip", entry.Outcome, entry.SkipKind)
	}
}

// TestProjectResultRefusesAnUnknownNode covers the third refusal: a
// result naming a node the compiled DAG does not hold. It is unreachable
// through runNode, and it is checked because the alternative to checking
// is a nil dereference inside the audit path of a run that was fine.
func TestProjectResultRefusesAnUnknownNode(t *testing.T) {
	r := newProjectionRun(t)
	if _, err := r.projectResult(NodeResult{NodeID: "tasks[99]"}); err == nil {
		t.Fatal("projectResult accepted a result naming a node the DAG does not hold")
	}
}

// TestProjectLevelNumbersOnlyWrittenEntries proves a refused result
// leaves no gap in Sequence. A gap would read as a lost row; the record
// of a refusal is the returned error, which recordLevel logs and counts.
func TestProjectLevelNumbersOnlyWrittenEntries(t *testing.T) {
	r := newProjectionRun(t)

	refused := NodeResult{NodeID: "tasks[0]"}
	refused.Err = errTestUntagged

	entries, err := r.projectLevel([][]NodeResult{
		{NodeResult{NodeID: "tasks[0]"}},
		{refused},
		{NodeResult{NodeID: "tasks[0]", Changed: true}},
	})
	if err == nil {
		t.Fatal("projectLevel reported no problem for a result it could not classify")
	}
	if len(entries) != 2 {
		t.Fatalf("projectLevel returned %d entries, want the two it could classify", len(entries))
	}
	if entries[0].Sequence != 1 || entries[1].Sequence != 2 {
		t.Errorf("projectLevel numbered the entries %d and %d, want a dense 1 and 2", entries[0].Sequence, entries[1].Sequence)
	}
	if entries[0].RunID != "run-under-test" {
		t.Errorf("projectLevel stamped RunID %q, want the run's own", entries[0].RunID)
	}
}

// errTestUntagged is the error these controls attach to a result. Its
// text is deliberately something a real error would never say, so a
// projection that ever started copying an error's message would show up
// here rather than in a passing assertion.
var errTestUntagged = testError("a device said something this journal must never store")

// testError is a minimal error type, avoiding an errors import for one
// sentinel.
type testError string

// Error implements error.
func (e testError) Error() string { return string(e) }

// TestProjectResultKeepsTheInverseWhenAPostActionStageFails is the
// regression control for the defect two independent review lenses found
// in this increment's first draft: a task that really changed the device
// and recorded an inverse journaled as "nothing to undo" whenever it then
// failed at register_mask or at record.
//
// The cause was that runOne assigned the exported NodeResult.Stats only
// on its success path, while both post-action failure returns happen with
// a fully populated ActionResult in hand. The projection read Stats, saw
// nil, and produced an entry naming no stats, no inverse and no diff, for
// exactly the run this phase exists to record. A confident wrong answer
// to the one question a rollback asks is worse than an absent entry.
//
// The fix is NodeResult.journalStats, a separate unexported field, rather
// than a widening of Stats. Widening Stats would have been a real leak:
// cmd/pleiades/run.go prints it under --verbose, and a failed
// register_mask means the author's own mask never applied, so printing
// the value it was written to protect is the exact disclosure the
// annotation exists to prevent. The journal can be shown what a printer
// must not be shown, because it stores key NAMES and never a value.
//
// So this asserts both halves: the names survive the failure, and no
// value does.
func TestProjectResultKeepsTheInverseWhenAPostActionStageFails(t *testing.T) {
	const secret = "correct-horse-battery-staple-and-then-some"

	for _, stage := range []FailureStage{FailureStageRegisterMask, FailureStageRecord} {
		t.Run(string(stage), func(t *testing.T) {
			// A real Collection method, deliberately not the shared
			// helper's noop: only a Collection method can call
			// sdk.RecordInverse, and the projection now refuses to read an
			// inverse out of an engine action's stats precisely so a
			// runbook cannot forge one. Using noop here would test the
			// forgery guard rather than this regression.
			r := &run{
				dag:   buildInternalDAG(t, `{"id": "post-action-failure", "tasks": [{"name": "a", "fqcn": "exec.command", "params": {"command": "true"}}]}`),
				runID: "run-under-test",
			}

			// What a real changed-then-failed node carries: the action
			// succeeded and recorded an inverse whose params hold the
			// prior file, and only then did the post-action stage fail.
			changed := NodeResult{NodeID: "tasks[0]", Device: "dev-1", Changed: true}
			changed.journalStats = map[string]interface{}{
				"inverse": map[string]any{
					"fqcn":   "noop",
					"params": map[string]any{"content": secret},
				},
			}
			changed.fail(stage, errTestUntagged)

			entry, err := r.projectResult(changed)
			if err != nil {
				t.Fatalf("projectResult refused a changed-then-failed node: %v", err)
			}

			if entry.InverseFQCN == "" {
				t.Error("the inverse was lost: a node that changed the device and recorded an undo " +
					"journaled as having nothing to undo, which is the defect this test exists for")
			}
			if len(entry.StatKeys) == 0 && entry.UndeclaredStatCount == 0 {
				t.Error("no stat key was named or counted, so the projection saw an empty stats map")
			}
			if entry.FailureStage != stage {
				t.Errorf("projected stage %q, want %q", entry.FailureStage, stage)
			}

			// The other half, and the reason journalStats is safe to
			// populate here at all: names travel, values do not.
			if strings.Contains(fmt.Sprintf("%+v", entry), secret) {
				t.Errorf("the entry carries the inverse's parameter VALUE, not just its key name: %+v", entry)
			}
		})
	}
}

// TestProjectResultRefusesAForgedInverseFromAnEngineAction is the control
// for the forgery guard the regression test above deliberately avoids.
//
// builtinActionExecutor sets a noop's stats to task.Params verbatim
// (action.go), so a runbook author writing an "inverse" param would, under
// an earlier draft, have had it projected as a genuinely recorded undo
// with a resolved InverseFQCN beside it. Only a Collection method can call
// sdk.RecordInverse, because only a Collection method is handed a
// RunbookContext to call it on, so an inverse in an engine action's stats
// never came from the SDK. A rollback engine is the eventual reader of
// InverseFQCN, which makes a forged one a correctness problem rather than
// an untidy record.
//
// The key is counted rather than dropped, so the entry still says
// something was there that this platform will not vouch for.
func TestProjectResultRefusesAForgedInverseFromAnEngineAction(t *testing.T) {
	r := newProjectionRun(t) // its task is a noop, which is the point here

	forged := NodeResult{NodeID: "tasks[0]", Device: "dev-1", Changed: true}
	forged.journalStats = map[string]interface{}{
		"inverse": map[string]any{
			"fqcn":   "exec.command",
			"params": map[string]any{"command": "rm -rf /"},
		},
	}

	entry, err := r.projectResult(forged)
	if err != nil {
		t.Fatalf("projectResult refused the node outright: %v", err)
	}
	if entry.InverseFQCN != "" {
		t.Errorf("a noop's own params were projected as a recorded undo naming %q; "+
			"a runbook must not be able to forge an inverse a rollback engine would trust", entry.InverseFQCN)
	}
	if entry.UndeclaredStatCount == 0 {
		t.Error("the forged key vanished silently; it must be counted, so the record still says " +
			"something was there this platform will not vouch for")
	}
}
