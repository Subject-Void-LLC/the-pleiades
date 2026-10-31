// Tests that a block's rollback_on_failure undoes what the block's tasks
// completed on a device, newest first, before its rescue, through the real
// Executor: which undos run where and in what order, what the journal says
// of them, and what a failed or impossible undo leaves.
package engine_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerRollbackFixtures registers rbtest.make, whose recorded undo is
// rbtest.unmake of the same name and is held exactly, and rbtest.blob,
// whose undo keeps its content out of the journal and so cannot be
// replayed.
func registerRollbackFixtures(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	none := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	params := collection.Doc{Params: []collection.Param{{Name: "name", Type: "any"}, {Name: "content", Type: "any"}}}
	for _, d := range []collection.Descriptor{
		{Name: "rbtest.unmake", Invoke: none, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "fixture"}, Doc: params}},
		{Name: "rbtest.make", Invoke: none, Manifest: collection.Manifest{Status: collection.StatusImplemented, Doc: params,
			Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rbtest.unmake", Record: []string{"name"}}}}}},
		{Name: "rbtest.blob", Invoke: none, Manifest: collection.Manifest{Status: collection.StatusImplemented, Doc: params,
			Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "rbtest.unmake", Record: []string{"name"}, Withhold: []string{"content"}}}}}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatalf("registering %s: %v", d.Name, err)
		}
	}
}

// undoable is an ActionExecutor for rollback tests: rbtest.make and
// rbtest.blob change the device and record their undo, everything else
// changes nothing, a call fails when fail names it ("<task name or
// unmake:<name>>@<device>"), and each device's calls are kept in order.
type undoable struct {
	fail map[string]bool
	// keepAndFail names calls that change the device, record an undo kept
	// through a failure (sdk.Inverse.KeepOnFailure), and then fail.
	keepAndFail map[string]bool
	mu          sync.Mutex
	ran         map[string][]string
	// became is the account each call was asked to run as ("<call>@<device>"),
	// absent for a call that runs as the login.
	became map[string]string
}

func (u *undoable) Execute(_ context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	device := ""
	if d != nil {
		device = d.Name()
	}
	call := task.Name
	if task.FQCN == "rbtest.unmake" {
		call = "unmake:" + strings.TrimSpace(asText(task.Params["name"]))
	}
	u.mu.Lock()
	if u.ran == nil {
		u.ran = map[string][]string{}
	}
	u.ran[device] = append(u.ran[device], call)
	if user, on := engine.BecomeFor(task); on {
		if u.became == nil {
			u.became = map[string]string{}
		}
		u.became[call+"@"+device] = user
	}
	u.mu.Unlock()
	if u.fail[call+"@"+device] {
		return engine.ActionResult{}, errors.New("scripted failure of " + call + "@" + device)
	}
	if u.keepAndFail[call+"@"+device] {
		return engine.ActionResult{Stats: map[string]any{
			sdk.StatInverse: map[string]any{sdk.InverseFQCNKey: "rbtest.unmake", sdk.InverseParamsKey: map[string]any{"name": task.Params["name"]}, sdk.InverseKeepOnFailureKey: true},
		}}, errors.New("scripted failure of " + call + "@" + device + " after its change")
	}
	if strings.HasPrefix(task.Name, "bare") {
		// A change that recorded no undo at all.
		return engine.ActionResult{Changed: true}, nil
	}
	switch task.FQCN {
	case "rbtest.make", "rbtest.blob", "rbtest.keep":
		params := map[string]any{"name": task.Params["name"]}
		if task.FQCN == "rbtest.blob" {
			params["content"] = "the prior content"
		}
		return engine.ActionResult{Changed: true, Stats: map[string]any{
			sdk.StatInverse: map[string]any{sdk.InverseFQCNKey: "rbtest.unmake", sdk.InverseParamsKey: params},
		}}, nil
	case "rbtest.unmake":
		return engine.ActionResult{Changed: true}, nil
	}
	return engine.ActionResult{}, nil
}

// calls returns device's calls, in order.
func (u *undoable) calls(device string) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.ran[device])
}

// asText is a param's value as text.
func asText(v any) string {
	s, _ := v.(string)
	return s
}

// rollbackBlock is a block over web with rollback_on_failure, three changes,
// a rescue, an always and a task after it.
const rollbackBlock = `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
      - name: b1
        rbtest.make: {name: one}
      - name: b2
        rbtest.make: {name: two}
    rescue:
      - name: r0
        noop: {}
    always:
      - name: a0
        noop: {}
  - name: after
    noop: {}
`

// runUndoable runs runbook on resolver's devices with actions, journaling
// into journal when it is not nil.
func runUndoable(t *testing.T, runbook string, resolver engine.TargetResolver, actions *undoable, opts ...engine.ExecutorOption) engine.RunResult {
	t.Helper()
	opts = append([]engine.ExecutorOption{engine.WithRenderer(render.New())}, opts...)
	x := engine.NewExecutor(resolver, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

// TestRollbackOnFailure_UndoesTheBlockNewestFirstBeforeTheRescue: b2 fails
// on web1, so web1 undoes b1 and then b0, before the rescue; web2, where
// nothing failed, undoes nothing. Each undo is journaled as a rollback of
// this very run, of the node and step it undid, and reported with the node
// it undoes; the run is rescued.
func TestRollbackOnFailure_UndoesTheBlockNewestFirstBeforeTheRescue(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b2@web1": true}}
	journal := &entriesJournal{}
	result := runUndoable(t, rollbackBlock, twoDevices(), actions, engine.WithJournal(journal))

	if got, want := actions.calls("web1"), []string{"b0", "b1", "b2", "unmake:one", "unmake:zero", "r0", "a0", "after"}; !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
	if got, want := actions.calls("web2"), []string{"b0", "b1", "b2", "a0", "after"}; !slices.Equal(got, want) {
		t.Errorf("web2 ran %v, want %v", got, want)
	}
	if result.HasErrors() || !result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v: %+v", result.HasErrors(), result.Rescued(), result.Nodes)
	}

	undo, ok := resultOf(result, "tasks[0].block[1].undo[0]", "web1")
	if !ok || undo.Err != nil || undo.Undo == nil || undo.Undo.Node != "tasks[0].block[1]" || undo.Undo.Method != "rbtest.unmake" {
		t.Errorf("b1's undo result = %+v (found %v)", undo, ok)
	}
	var undos []engine.JournalEntry
	for _, e := range journal.entries {
		if e.UndoesNode != "" {
			undos = append(undos, e)
		}
	}
	if len(undos) != 2 {
		t.Fatalf("journaled %d undo entries, want 2: %+v", len(undos), undos)
	}
	for i, want := range []string{"tasks[0].block[1]", "tasks[0].block[0]"} {
		e := undos[i]
		if e.UndoesNode != want || e.NodeID != want+".undo[0]" || e.RollbackOf != result.RunID || e.FQCN != "rbtest.unmake" || e.Outcome != engine.OutcomeChanged {
			t.Errorf("undo entry %d = node %s undoes %s of %s, %s %s; want %s.undo[0] undoing %s of this run (%s), rbtest.unmake changed",
				i, e.NodeID, e.UndoesNode, e.RollbackOf, e.FQCN, e.Outcome, want, want, result.RunID)
		}
	}
}

// TestRollbackOnFailure_AFailedUndoStopsTheRescueAndFailsTheRun: the undo
// of b1 fails on web1, so b0 is not undone after it, the rescue does not
// run there, always does, and the run fails with the block's failure and
// the undo's; nothing after the block runs.
func TestRollbackOnFailure_AFailedUndoStopsTheRescueAndFailsTheRun(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b2@web1": true, "unmake:one@web1": true}}
	result := runUndoable(t, rollbackBlock, twoDevices(), actions)

	if got, want := actions.calls("web1"), []string{"b0", "b1", "b2", "unmake:one", "a0"}; !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
	if !result.HasErrors() || result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v; want a failed run", result.HasErrors(), result.Rescued())
	}
	rescue, ok := resultOf(result, "tasks[0].rescue[0]", "web1")
	if !ok || !rescue.Skipped || !strings.Contains(rescue.SkipReason, "undoing what the block completed") {
		t.Errorf("web1's rescue = %+v, want skipped because the undo did not finish", rescue)
	}
	if undo, _ := resultOf(result, "tasks[0].block[1].undo[0]", "web1"); undo.Err == nil {
		t.Errorf("the failed undo is not reported: %+v", undo)
	}
}

// TestRollbackOnFailure_WithoutARescueUndoesAndTheFailureGoesOn: a block
// with rollback_on_failure and nothing else undoes web1's completed tasks
// when b1 fails there, then the failure ends the run as it always did.
func TestRollbackOnFailure_WithoutARescueUndoesAndTheFailureGoesOn(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b1@web1": true}}
	result := runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
      - name: b1
        rbtest.make: {name: one}
      - name: b2
        rbtest.make: {name: two}
  - name: after
    noop: {}
`, twoDevices(), actions)

	if got, want := actions.calls("web1"), []string{"b0", "b1", "unmake:zero"}; !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
	if got := actions.calls("web2"); slices.Contains(got, "after") || slices.Contains(got, "unmake:zero") {
		t.Errorf("web2 ran %v: the run went on, or undid a device where nothing failed", got)
	}
	if !result.HasErrors() {
		t.Error("the failure did not fail the run")
	}
}

// TestRollbackOnFailure_NestedBlocksUndoEachChangeOnce: the inner block
// undoes its own change when its task fails; the failure goes on to the
// outer block, which undoes its change and not the inner one's again, and
// then rescues.
func TestRollbackOnFailure_NestedBlocksUndoEachChangeOnce(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"i1@web1": true}}
	result := runUndoable(t, `id: rb
hosts: web
tasks:
  - name: outer
    rollback_on_failure: true
    block:
      - name: o0
        rbtest.make: {name: outer-zero}
      - name: inner
        rollback_on_failure: true
        block:
          - name: i0
            rbtest.make: {name: inner-zero}
          - name: i1
            rbtest.make: {name: inner-one}
    rescue:
      - name: r0
        noop: {}
`, twoDevices(), actions)

	if got, want := actions.calls("web1"), []string{"o0", "i0", "i1", "unmake:inner-zero", "unmake:outer-zero", "r0"}; !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
	if result.HasErrors() || !result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v", result.HasErrors(), result.Rescued())
	}
}

// TestRollbackOnFailure_AnAuthoredRollbackRunsInPlaceOfTheRecordedUndo: a
// task's own rollback: list is its undo, as it is for pleiades rollback.
func TestRollbackOnFailure_AnAuthoredRollbackRunsInPlaceOfTheRecordedUndo(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b1@web1": true}}
	runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
        rollback:
          - rbtest.unmake: {name: "authored-{{ vars.suffix | default('zero') }}"}
      - name: b1
        rbtest.make: {name: one}
    rescue:
      - name: r0
        noop: {}
`, twoDevices(), actions)

	if got, want := actions.calls("web1"), []string{"b0", "b1", "unmake:authored-zero", "r0"}; !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
}

// TestRollbackOnFailure_AnUndoTheJournalCannotHoldStopsTheRescue: b0's undo
// keeps a value out of the journal, so it cannot be replayed exactly; the
// change is reported as not undone, and the rescue does not run.
func TestRollbackOnFailure_AnUndoTheJournalCannotHoldStopsTheRescue(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b1@web1": true}}
	result := runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.blob: {name: zero}
      - name: b1
        rbtest.make: {name: one}
    rescue:
      - name: r0
        noop: {}
    always:
      - name: a0
        noop: {}
`, twoDevices(), actions)

	if got, want := actions.calls("web1"), []string{"b0", "b1", "a0"}; !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
	refused, ok := resultOf(result, "tasks[0].block[0].undo[0]", "web1")
	if !ok || refused.Err == nil || !strings.Contains(refused.Err.Error(), "cannot be replayed") {
		t.Errorf("the change that could not be undone = %+v (found %v)", refused, ok)
	}
	if !result.HasErrors() {
		t.Error("the run did not fail")
	}
}

// TestRollbackOnFailure_AUnitBlockUndoesEveryDevice: a block reaching
// another device runs as one unit, so a failure on its other device undoes
// what the block completed on every device.
func TestRollbackOnFailure_AUnitBlockUndoesEveryDevice(t *testing.T) {
	registerRollbackFixtures(t)
	resolver := twoDevices()
	resolver["db"] = []inventory.InventoryItem{&inventorytest.Stub{StubID: "db1", StubName: "db1", StubState: inventory.StateActive}}
	actions := &undoable{fail: map[string]bool{"grant@db1": true}}
	result := runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
      - name: grant
        rbtest.make: {name: grant, target: db}
    rescue:
      - name: r0
        noop: {}
`, resolver, actions)

	for _, device := range []string{"web1", "web2"} {
		if got := actions.calls(device); !slices.Contains(got, "unmake:zero") {
			t.Errorf("%s ran %v, want b0 undone there too", device, got)
		}
	}
	if result.HasErrors() || !result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v", result.HasErrors(), result.Rescued())
	}
}

// TestRollbackOnFailure_ATargetThatResolvesToNothingUndoesEveryDevice: a
// task whose target matches no device fails the block everywhere, with no
// device to handle it on, so what the block completed is undone on every
// device before the run ends.
func TestRollbackOnFailure_ATargetThatResolvesToNothingUndoesEveryDevice(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{}
	result := runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
      - name: lost
        rbtest.make: {name: lost, target: nowhere}
`, twoDevices(), actions)

	for _, device := range []string{"web1", "web2"} {
		if got, want := actions.calls(device), []string{"b0", "unmake:zero"}; !slices.Equal(got, want) {
			t.Errorf("%s ran %v, want %v", device, got, want)
		}
	}
	if !result.HasErrors() {
		t.Error("the run did not fail")
	}
}

// TestRollbackOnFailure_ACheckUndoesNothing: a check changes nothing, so it
// has nothing to undo.
func TestRollbackOnFailure_ACheckUndoesNothing(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b2@web1": true}}
	runUndoable(t, rollbackBlock, twoDevices(), actions, engine.WithMode(collection.ModeCheck))
	for _, device := range []string{"web1", "web2"} {
		for _, call := range actions.calls(device) {
			if strings.HasPrefix(call, "unmake:") {
				t.Errorf("%s undid something in a check: %v", device, actions.calls(device))
			}
		}
	}
}

// TestRollbackOnFailure_BelongsOnABlock: on a single task the key is
// refused, naming the task's own rollback: list as what it means instead.
func TestRollbackOnFailure_BelongsOnABlock(t *testing.T) {
	_, err := buildYAML(t, `id: rb
hosts: web
tasks:
  - name: alone
    rollback_on_failure: true
    noop: {}
`)
	if err == nil || !strings.Contains(err.Error(), "rollback_on_failure without a block") || !strings.Contains(err.Error(), "rollback: list") {
		t.Errorf("build = %v, want a refusal naming the block and the rollback: list", err)
	}
}

// TestRollbackOnFailure_AChangeWithNoUndoIsNotUndone: a task that changed
// the device and recorded no undo is reported as not undone, the rescue
// does not run, and the run fails; and when the block failed on every
// device at once, an undo that fails ends the run there too.
func TestRollbackOnFailure_AChangeWithNoUndoIsNotUndone(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b1@web1": true}}
	result := runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: bare b0
        rbtest.make: {name: zero}
      - name: b1
        rbtest.make: {name: one}
    rescue:
      - name: r0
        noop: {}
`, twoDevices(), actions)
	refused, ok := resultOf(result, "tasks[0].block[0].undo[0]", "web1")
	if !ok || refused.Err == nil || !strings.Contains(refused.Err.Error(), "recorded no undo") || slices.Contains(actions.calls("web1"), "r0") {
		t.Errorf("refused %+v (found %v); web1 ran %v", refused, ok, actions.calls("web1"))
	}

	everywhere := &undoable{fail: map[string]bool{"unmake:zero@web2": true}}
	result = runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
      - name: lost
        rbtest.make: {name: lost, target: nowhere}
`, twoDevices(), everywhere)
	if failed, _ := resultOf(result, "tasks[0].block[0].undo[0]", "web2"); failed.Err == nil || !result.HasErrors() {
		t.Errorf("web2's failed undo %+v, HasErrors %v", failed, result.HasErrors())
	}
}

// TestRollbackOnFailure_AnUndoRunsAsTheAccountTheChangeRanAs: inside a block that
// becomes an account, the undo of a completed change (the recorded one, and an
// authored rollback: step) runs as that account, as a later pleiades rollback
// replays it, not as the login.
func TestRollbackOnFailure_AnUndoRunsAsTheAccountTheChangeRanAs(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b2@web1": true}}
	runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    become: true
    become_user: svc
    block:
      - name: b0
        rbtest.make: {name: zero}
      - name: b1
        rbtest.make: {name: one}
        rollback:
          - name: own1
            rbtest.unmake: {name: one}
      - name: b2
        rbtest.make: {name: two}
    rescue:
      - name: r0
        noop: {}
`, twoDevices(), actions)
	actions.mu.Lock()
	defer actions.mu.Unlock()
	// b0's undo is the recorded one, b1's is its authored rollback: step; the
	// harness names both by what they unmake.
	for _, call := range []string{"unmake:zero@web1", "unmake:one@web1"} {
		if actions.became[call] != "svc" {
			t.Errorf("%s ran as %q, want svc (all: %v)", call, actions.became[call], actions.became)
		}
	}
	// The rescue is a task of the block too and inherits it; a call outside
	// any become runs as the login.
	if actions.became["r0@web1"] != "svc" {
		t.Errorf("the rescue ran as %q", actions.became["r0@web1"])
	}
}
