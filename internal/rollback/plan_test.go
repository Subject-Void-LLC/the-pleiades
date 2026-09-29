// Tests for the rollback planner (plan.go, steps.go, history.go), over
// fixture methods registered for each undo shape the planner tells apart.
package rollback_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerShapes registers one fixture method per undo shape the planner
// tells apart, for the rest of the test.
func registerShapes(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	params := []collection.Param{{Name: "name"}, {Name: "uuid"}, {Name: "content"}, {Name: "size"}}
	for name, r := range map[string]collection.Reversibility{
		"rb.delete": {Notes: "gone is gone"},
		"rb.stop":   {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rb.start", Record: []string{"name"}}}},
		"rb.start":  {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rb.stop", Record: []string{"name"}}}},
		"rb.make":   {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rb.delete", Record: []string{"name", "uuid"}}}},
		"rb.edit":   {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rb.edit", Record: []string{"name"}, Withhold: []string{"content"}}}},
		"rb.copy":   {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rb.edit", Record: []string{"name"}, MayBePartial: true}}},
		"rb.read":   {Notes: "reads", ReadOnly: true},
		"rb.shell":  {Notes: "an arbitrary command"},
	} {
		d := collection.Descriptor{Name: name, Invoke: run, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: r, Doc: collection.Doc{Params: params}}}
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
}

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// entry builds one journal entry of a run. seq also orders its times.
func entry(run, node, device string, seq int, fqcn string, outcome engine.Outcome) engine.JournalEntry {
	return engine.JournalEntry{
		RunID: run, NodeID: node, DeviceID: device, Sequence: seq, FQCN: fqcn, Outcome: outcome,
		ActionChanged: outcome == engine.OutcomeChanged, TaskName: "task " + node,
		StartedAt: t0.Add(time.Duration(seq) * time.Minute), FinishedAt: t0.Add(time.Duration(seq)*time.Minute + time.Second),
	}
}

// undo gives e a recorded undo through fqcn with params, all recorded.
func undo(e engine.JournalEntry, fqcn string, params map[string]any) engine.JournalEntry {
	e.InverseFQCN = fqcn
	for _, k := range sortedKeys(params) {
		e.InverseParamKeys = append(e.InverseParamKeys, k)
		p := engine.InverseParam{Key: k}
		switch v := params[k].(type) {
		case string:
			p.Text = &v
		case int:
			n := json.Number(strconv.Itoa(v))
			p.Number = &n
		case bool:
			p.Bool = &v
		}
		e.InverseParams = append(e.InverseParams, p)
	}
	e.InverseComplete = true
	return e
}

func sortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}

// inventoryOf resolves device ids named here.
func inventoryOf(ids ...string) func(string) (string, bool) {
	return func(id string) (string, bool) {
		for _, known := range ids {
			if known == id {
				return "name-of-" + id, true
			}
		}
		return "", false
	}
}

// createRun is a run that made a VM on the host h1, then started it.
func createRun() rollback.Run {
	return rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		undo(entry("run-A", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "bsd", "uuid": "u-1"}),
		undo(entry("run-A", "tasks[1]", "h1", 2, "rb.start", engine.OutcomeChanged), "rb.stop", map[string]any{"name": "bsd"}),
		entry("run-A", "tasks[2]", "h1", 3, "rb.read", engine.OutcomeRan),
	}}
}

// refused returns the refusal's problems, failing when there is none.
func refused(t *testing.T, err error) []rollback.Problem {
	t.Helper()
	var r *rollback.RefusedError
	if !errors.As(err, &r) {
		t.Fatalf("Build = %v, want a refusal", err)
	}
	return r.Problems
}

func TestBuild_UndoesEveryChangeInReverse(t *testing.T) {
	registerShapes(t)
	plan, err := rollback.Build(rollback.Request{Target: createRun(), Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	steps := plan.Steps()
	if len(steps) != 2 || steps[0].FQCN != "rb.stop" || steps[1].FQCN != "rb.delete" {
		t.Fatalf("steps %+v, want the start undone before the make", steps)
	}
	if steps[1].Params["name"] != "bsd" || steps[1].Params["uuid"] != "u-1" || steps[1].DeviceName != "name-of-h1" || steps[1].Source != "recorded" {
		t.Errorf("the delete step %+v does not carry the recorded identity", steps[1])
	}
	if plan.UnacceptedUnknown != 0 || len(plan.Unknown) != 0 {
		t.Errorf("a sealed run with no failure reports unknowns: %+v", plan.Unknown)
	}
}

func TestBuild_RefusesAChangeItCannotUndoExactly(t *testing.T) {
	registerShapes(t)
	withheld := undo(entry("run-A", "tasks[0]", "h1", 1, "rb.edit", engine.OutcomeChanged), "rb.edit", map[string]any{"name": "f"})
	withheld.InverseParamKeys = append(withheld.InverseParamKeys, "content")
	withheld.InverseComplete = false
	partial := undo(entry("run-A", "tasks[1]", "h1", 2, "rb.copy", engine.OutcomeChanged), "rb.edit", map[string]any{"name": "f"})
	partial.InversePartial, partial.InverseComplete = true, false
	for _, tc := range []struct {
		name   string
		entry  engine.JournalEntry
		want   string
		accept string
		fix    func(*rollback.Request)
	}{
		{"no undo recorded", entry("run-A", "tasks[0]", "h1", 1, "rb.shell", engine.OutcomeChanged),
			"cannot be undone (an arbitrary command)", "--leave tasks[0]", func(r *rollback.Request) { r.Leave = map[string]bool{"tasks[0]": true} }},
		{"an undo withheld", withheld, "content kept out of the journal", "--leave tasks[0]",
			func(r *rollback.Request) { r.Leave = map[string]bool{"tasks[0]": true} }},
		{"a partial undo", partial, "is partial", "--allow-partial tasks[1]",
			func(r *rollback.Request) { r.AllowPartial = map[string]bool{"tasks[1]": true} }},
		{"a device gone", undo(entry("run-A", "tasks[0]", "gone", 1, "rb.start", engine.OutcomeChanged), "rb.stop", map[string]any{"name": "x"}),
			"no longer in the inventory", "--leave tasks[0]", func(r *rollback.Request) { r.Leave = map[string]bool{"tasks[0]": true} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := rollback.Request{Target: rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{tc.entry}}, Devices: inventoryOf("h1")}
			_, err := rollback.Build(req)
			problems := refused(t, err)
			if len(problems) != 1 || !strings.Contains(problems[0].Reason, tc.want) || problems[0].Accept != tc.accept {
				t.Fatalf("problems %+v, want one mentioning %q accepted by %q", problems, tc.want, tc.accept)
			}
			// Naming it is what accepts it, and nothing else does.
			tc.fix(&req)
			if _, err := rollback.Build(req); err != nil {
				t.Errorf("with %s named, Build = %v", tc.accept, err)
			}
		})
	}
}

func TestBuild_AFailedActionIsUnknownUnlessItOnlyReads(t *testing.T) {
	registerShapes(t)
	run := createRun()
	run.Entries = append(run.Entries,
		func() engine.JournalEntry {
			e := entry("run-A", "tasks[3]", "h1", 4, "rb.shell", engine.OutcomeFailed)
			e.FailureStage = engine.FailureStageAction
			return e
		}(),
		func() engine.JournalEntry {
			e := entry("run-A", "tasks[4]", "h1", 5, "rb.read", engine.OutcomeFailed)
			e.FailureStage = engine.FailureStageAction
			return e
		}())
	plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Unknown) != 1 || plan.Unknown[0].Node != "tasks[3]" || plan.UnacceptedUnknown != 1 {
		t.Fatalf("unknown %+v (%d unaccepted), want only the command that failed", plan.Unknown, plan.UnacceptedUnknown)
	}
	plan, _ = rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1"), AllowUnknown: map[string]bool{"tasks[3]": true}})
	if plan.UnacceptedUnknown != 0 {
		t.Errorf("an accepted unknown still counts")
	}
	run.Sealed = false
	plan, _ = rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1"), AllowUnknown: map[string]bool{"tasks[3]": true}})
	if plan.UnacceptedUnknown != 1 || plan.Unknown[len(plan.Unknown)-1].Node != rollback.Unsealed {
		t.Errorf("an unsealed journal is not an unknown: %+v", plan.Unknown)
	}
}

func TestBuild_ResumesAndThenHasNothingLeft(t *testing.T) {
	registerShapes(t)
	partway := rollback.Run{ID: "run-R", Sealed: true, Entries: []engine.JournalEntry{
		func() engine.JournalEntry {
			e := entry("run-R", "tasks[0]", "h1", 10, "rb.stop", engine.OutcomeChanged)
			e.RollbackOf, e.UndoesNode = "run-A", "tasks[1]"
			return e
		}(),
		func() engine.JournalEntry {
			e := entry("run-R", "tasks[1]", "h1", 11, "rb.delete", engine.OutcomeFailed)
			e.RollbackOf, e.UndoesNode, e.FailureStage = "run-A", "tasks[0]", engine.FailureStageAction
			return e
		}(),
	}}
	plan, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{partway}, Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if steps := plan.Steps(); len(steps) != 1 || steps[0].FQCN != "rb.delete" || len(plan.AlreadyUndone) != 1 {
		t.Fatalf("resumed plan %+v, want only the delete, the stop already undone", plan)
	}
	// What is left named to leave, the rest undone: nothing to do, and it
	// says the changes were undone rather than all left in place.
	_, err = rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{partway}, Devices: inventoryOf("h1"), Leave: map[string]bool{"tasks[0]": true}})
	if problems := refused(t, err); !strings.Contains(problems[0].Reason, "already been undone") || !strings.Contains(problems[0].Reason, "named to leave") {
		t.Errorf("the rest left, the others undone: %+v", problems)
	}

	finished := partway
	finished.Entries = append([]engine.JournalEntry(nil), partway.Entries...)
	finished.Entries[1].Outcome, finished.Entries[1].FailureStage = engine.OutcomeChanged, ""
	_, err = rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{finished}, Devices: inventoryOf("h1")})
	if problems := refused(t, err); !strings.Contains(problems[0].Reason, "already been undone") {
		t.Errorf("a run rolled back in full was not refused as already undone: %+v", problems)
	}
	// And a rollback itself is not something to roll back.
	_, err = rollback.Build(rollback.Request{Target: finished, Devices: inventoryOf("h1")})
	if problems := refused(t, err); !strings.Contains(problems[0].Reason, "is a rollback of run-A") {
		t.Errorf("rolling back a rollback was not refused: %+v", problems)
	}
}

func TestBuild_RefusesUndoingBeneathALaterRun(t *testing.T) {
	registerShapes(t)
	later := rollback.Run{ID: "run-B", Sealed: true, Entries: []engine.JournalEntry{
		undo(entry("run-B", "tasks[0]", "h1", 20, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "bsd"}),
	}}
	_, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later}, Devices: inventoryOf("h1")})
	problems := refused(t, err)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "run run-B changed name-of-h1") || problems[0].Accept != "--despite-run run-B" {
		t.Fatalf("problems %+v, want run-B named with its flag", problems)
	}
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later}, Devices: inventoryOf("h1"),
		DespiteRuns: map[string]bool{"run-B": true}}); err != nil {
		t.Errorf("with --despite-run run-B, Build = %v", err)
	}
	// Once run-B is rolled back in full, it and its rollback cancel out.
	undoB := rollback.Run{ID: "run-RB", Sealed: true, Entries: []engine.JournalEntry{func() engine.JournalEntry {
		e := entry("run-RB", "tasks[0]", "h1", 30, "rb.start", engine.OutcomeChanged)
		e.RollbackOf, e.UndoesNode = "run-B", "tasks[0]"
		return e
	}()}}
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later, undoB}, Devices: inventoryOf("h1")}); err != nil {
		t.Errorf("after run-B was rolled back, Build = %v", err)
	}
	// Rolled back in full on a second attempt, after a first that failed
	// (found in the lab: a VM that did not answer its power button in
	// time), run-B still cancels out; and one whose latest attempt failed
	// still counts.
	failedFirst := rollback.Run{ID: "run-RB0", Sealed: true, Entries: []engine.JournalEntry{func() engine.JournalEntry {
		e := entry("run-RB0", "tasks[0]", "h1", 25, "rb.start", engine.OutcomeFailed)
		e.RollbackOf, e.UndoesNode, e.FailureStage = "run-B", "tasks[0]", engine.FailureStageAction
		return e
	}()}}
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later, failedFirst, undoB}, Devices: inventoryOf("h1")}); err != nil {
		t.Errorf("after run-B was rolled back on a second attempt, Build = %v", err)
	}
	failedLast := failedFirst
	failedLast.Entries = []engine.JournalEntry{failedFirst.Entries[0]}
	failedLast.Entries[0].StartedAt, failedLast.Entries[0].FinishedAt = t0.Add(40*time.Minute), t0.Add(41*time.Minute)
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later, undoB, failedLast}, Devices: inventoryOf("h1")}); err == nil {
		t.Error("run-B, whose latest rollback attempt failed, did not count as a later change")
	}
	// A later run on another device is none of this run's business.
	elsewhere := later
	elsewhere.Entries = []engine.JournalEntry{undo(entry("run-B", "tasks[0]", "h2", 20, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "x"})}
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{elsewhere}, Devices: inventoryOf("h1", "h2")}); err != nil {
		t.Errorf("a later run on another device refused the rollback: %v", err)
	}
}

// TestBuild_RefusesAJournalRecordItDidNotWrite covers a journal someone
// edited, or a row a Runner forged: the recorded undo is held to the
// method's own declaration, never trusted as written.
func TestBuild_RefusesAJournalRecordItDidNotWrite(t *testing.T) {
	registerShapes(t)
	escape := "bsd\x1b[2J"
	for _, tc := range []struct {
		name string
		edit func(*engine.JournalEntry)
	}{
		{"an undo swapped to another method", func(e *engine.JournalEntry) { e.InverseFQCN = "rb.shell" }},
		{"the device selector added", func(e *engine.JournalEntry) {
			v := "db"
			e.InverseParams = append(e.InverseParams, engine.InverseParam{Key: "target", Text: &v})
		}},
		{"a value under an undeclared key", func(e *engine.JournalEntry) {
			v := "x"
			e.InverseParams = append(e.InverseParams, engine.InverseParam{Key: "content", Text: &v})
		}},
		{"an escape sequence", func(e *engine.JournalEntry) { e.InverseParams[0].Text = &escape }},
		{"an emitter this build does not have", func(e *engine.JournalEntry) { e.FQCN = "acme.anything" }},
		{"partial where the method never is", func(e *engine.JournalEntry) { e.InversePartial = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := createRun()
			tc.edit(&run.Entries[0])
			_, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Node != "tasks[0]" {
				t.Fatalf("problems %+v, want one naming tasks[0]", problems)
			}
		})
	}
}

func TestBuild_UsesTheAuthoredUndo(t *testing.T) {
	registerShapes(t)
	run := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		func() engine.JournalEntry {
			e := entry("run-A", "tasks[0]", "h1", 1, "rb.shell", engine.OutcomeChanged)
			e.AuthoredRollback = true
			return e
		}(),
	}}
	req := rollback.Request{Target: run, Devices: inventoryOf("h1")}
	if _, err := rollback.Build(req); err == nil || !strings.Contains(refused(t, err)[0].Reason, "--runbook") {
		t.Fatalf("an authored undo with no runbook was not refused asking for one: %v", err)
	}
	req.Authored = func(node string) ([]engine.Task, error) {
		return []engine.Task{
			{Name: "stop", FQCN: "rb.stop", Params: map[string]any{"name": "svc"}},
			{Name: "clean", FQCN: "rb.delete", Params: map[string]any{"name": "svc"}},
		}, nil
	}
	plan, err := rollback.Build(req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Levels) != 2 || plan.Levels[0][0].FQCN != "rb.stop" || plan.Levels[1][0].Index != 1 || plan.Levels[0][0].Source != "authored" {
		t.Errorf("levels %+v, want the two authored steps in order", plan.Levels)
	}
}

// TestBuild_UsesARollbackListWrittenAfterTheRun covers a runbook given a
// rollback: list after the run, so the journal does not say the task had
// one: the list wins over a recorded undo, a missing runbook falls back to
// the recorded undo, and any other failure to read it blocks that node.
func TestBuild_UsesARollbackListWrittenAfterTheRun(t *testing.T) {
	registerShapes(t)
	run := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		entry("run-A", "tasks[0]", "h1", 1, "rb.shell", engine.OutcomeChanged),
		undo(entry("run-A", "tasks[1]", "h1", 2, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "bsd", "uuid": "u-1"}),
	}}
	authored := map[string][]engine.Task{
		"tasks[0]": {{Name: "clean", FQCN: "rb.delete", Params: map[string]any{"name": "marker"}}},
		"tasks[1]": {{Name: "stop first", FQCN: "rb.stop", Params: map[string]any{"name": "bsd"}}},
	}
	req := rollback.Request{Target: run, Devices: inventoryOf("h1"), Authored: func(node string) ([]engine.Task, error) {
		return authored[node], nil
	}}
	plan, err := rollback.Build(req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	steps := plan.Steps()
	if len(steps) != 2 || steps[0].FQCN != "rb.stop" || steps[0].Source != "authored" || steps[1].FQCN != "rb.delete" || steps[1].Params["name"] != "marker" {
		t.Errorf("steps %+v, want both nodes undone by their rollback: lists, newest first", steps)
	}

	// Only the command has a list now: the create keeps its recorded undo.
	delete(authored, "tasks[1]")
	plan, err = rollback.Build(req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if steps := plan.Steps(); len(steps) != 2 || steps[0].Source != "recorded" || steps[0].Params["uuid"] != "u-1" {
		t.Errorf("steps %+v, want the create undone as recorded", steps)
	}

	// No runbook: the recorded undo still runs, and the command's refusal
	// says why no list was found.
	req.Authored = func(string) ([]engine.Task, error) {
		return nil, fmt.Errorf("%w: none in runbooks/", rollback.ErrNoRunbook)
	}
	problems := refused(t, func() error { _, err := rollback.Build(req); return err }())
	if len(problems) != 1 || problems[0].Node != "tasks[0]" || !strings.Contains(problems[0].Reason, "none in runbooks/") {
		t.Errorf("problems %+v, want only the command refused, naming the missing runbook", problems)
	}

	// Any other failure to read the runbook is the node's problem, even
	// where a recorded undo exists: the operator named a runbook, and it
	// is not the one that ran.
	req.Authored = func(string) ([]engine.Task, error) { return nil, errors.New("--runbook x.yaml has changed since") }
	if problems := refused(t, func() error { _, err := rollback.Build(req); return err }()); len(problems) != 2 {
		t.Errorf("problems %+v, want both nodes refused", problems)
	}
}

// TestBuild_UndoesTheLatestAttemptAndAControllerSideChange covers the
// Walk tier's shapes: one node reporting a change in two attempts is
// undone once, and a change no device holds runs controller-side.
func TestBuild_UndoesTheLatestAttemptAndAControllerSideChange(t *testing.T) {
	registerShapes(t)
	first := undo(entry("job-1", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "a", "uuid": "old"})
	second := undo(entry("job-1", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "a", "uuid": "new"})
	second.Attempt = 2
	cloud := undo(entry("job-1", "tasks[1]", "", 2, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "bucket", "uuid": "b"})
	plan, err := rollback.Build(rollback.Request{Target: rollback.Run{ID: "job-1", Sealed: true, Entries: []engine.JournalEntry{first, second, cloud}}, Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var uuids []any
	for _, s := range plan.Steps() {
		uuids = append(uuids, s.Params["uuid"])
	}
	if len(uuids) != 2 || uuids[0] != "new" || uuids[1] != "b" {
		t.Errorf("undid %v, want the latest attempt's make once and the controller-side one", uuids)
	}
}
