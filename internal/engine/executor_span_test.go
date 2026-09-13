// This file covers the two temporal widenings Phase 40's run journal
// needs, and it covers them through the real compile-and-run path rather
// than by constructing the widened types directly, because the claim
// being tested is about the executor's own construction sites and not
// about the struct fields.
//
// NodeResult.StartedAt/FinishedAt: every result the executor produces for
// a node that executed something carries both, and the one shape that
// executed nothing (the synthetic parallel fan-out/join marker) carries
// neither. A site left unstamped is a silent hole in the journal, so the
// assertion is over every result a run produced, not over a chosen few.
//
// ConditionResult.Ordinal/Total: the numbers match what the Reason
// sentence already says, and the sentence itself is unchanged. Both
// halves matter. Other things read that text.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// failingActionExecutor is a test-only ActionExecutor that always fails,
// used to reach the executor's failure returns, which must bound their
// result exactly as a successful one does: a failed task is the case the
// journal exists for, so it is the last one that may lose its duration.
type failingActionExecutor struct{}

func (failingActionExecutor) Execute(_ context.Context, _ *engine.Task, _ inventory.InventoryItem) (engine.ActionResult, error) {
	return engine.ActionResult{}, errors.New("action refused by the test double")
}

// assertSpanned fails unless n's bounds are a real, ordered interval that
// falls inside the wall-clock window the caller bracketed Run with. The
// window check is what makes this more than a non-zero test: a field set
// from the wrong clock, or copied from another result, lands outside it.
func assertSpanned(t *testing.T, n engine.NodeResult, before, after time.Time) {
	t.Helper()
	if n.StartedAt.IsZero() || n.FinishedAt.IsZero() {
		t.Errorf("node %q (device %q): expected both bounds set, got StartedAt=%v FinishedAt=%v", n.NodeID, n.Device, n.StartedAt, n.FinishedAt)
		return
	}
	if n.FinishedAt.Before(n.StartedAt) {
		t.Errorf("node %q: FinishedAt %v precedes StartedAt %v", n.NodeID, n.FinishedAt, n.StartedAt)
	}
	if n.StartedAt.Before(before) || n.FinishedAt.After(after) {
		t.Errorf("node %q: span [%v, %v] falls outside the run's own window [%v, %v]", n.NodeID, n.StartedAt, n.FinishedAt, before, after)
	}
	// UTC everywhere, matching publish's own clock, so a journal row from
	// a Runner in one zone and a CLI in another sort against each other.
	if n.StartedAt.Location() != time.UTC || n.FinishedAt.Location() != time.UTC {
		t.Errorf("node %q: expected UTC bounds, got StartedAt in %v and FinishedAt in %v", n.NodeID, n.StartedAt.Location(), n.FinishedAt.Location())
	}
}

// TestNodeResultBoundsEveryExecutedNode runs one DAG shaped to reach five
// of the executor's nine NodeResult construction sites at once: the
// synthetic parallel markers, a condition that evaluates false, a
// lifecycle-inadmissible device, and a device that really runs. It then
// asserts over every result the run produced, so a site nobody thought to
// name still has to satisfy the rule.
func TestNodeResultBoundsEveryExecutedNode(t *testing.T) {
	active := &inventorytest.Stub{StubID: "active-host", StubName: "active-host", StubState: inventory.StateActive}
	quarantined := &inventorytest.Stub{StubID: "quarantined-host", StubName: "quarantined-host", StubState: inventory.StateQuarantined}
	resolver := mapResolver{"fleet": {active, quarantined}}

	dag := buildDAG(t, `{
		"id": "spans",
		"tasks": [
			{"name": "fanout", "parallel": [
				{"name": "p0", "fqcn": "noop"},
				{"name": "p1", "fqcn": "noop"}
			]},
			{"name": "skipme", "fqcn": "noop", "when_cel": "false"},
			{"name": "work", "fqcn": "noop", "params": {"target": "fleet"}}
		]
	}`)

	x := engine.NewExecutor(resolver, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	before := time.Now().UTC()
	result, err := x.Run(context.Background(), dag)
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}

	// The two synthetic markers this DAG's one parallel block synthesizes.
	// They short-circuit ahead of the whole pipeline and execute nothing,
	// so a bound on either would be a fabricated instant.
	synthetic := map[string]bool{"tasks[0].fanout": true, "tasks[0].join": true}

	var executed, markers int
	for _, n := range result.Nodes {
		if synthetic[n.NodeID] {
			markers++
			if !n.StartedAt.IsZero() || !n.FinishedAt.IsZero() {
				t.Errorf("node %q never ran, so both bounds must stay zero, got StartedAt=%v FinishedAt=%v", n.NodeID, n.StartedAt, n.FinishedAt)
			}
			continue
		}
		executed++
		assertSpanned(t, n, before, after)
	}

	if markers != 2 {
		t.Errorf("expected both synthetic parallel markers in the result, got %d", markers)
	}
	// p0, p1, skipme, and one result per resolved device for "work".
	if executed != 5 {
		t.Fatalf("expected 5 executed-node results, got %d: %+v", executed, result.Nodes)
	}

	// The skip and the lifecycle skip are the two results most easily left
	// unstamped, because neither reaches an action at all, so they are
	// named individually rather than trusted to the sweep above.
	byID := map[string]engine.NodeResult{}
	for _, n := range result.Nodes {
		byID[n.NodeID+"|"+n.Device] = n
	}
	if n, ok := byID["tasks[1]|"]; !ok || !n.Skipped {
		t.Fatalf("expected tasks[1] to be condition-skipped, got %+v", n)
	} else {
		assertSpanned(t, n, before, after)
	}
	if n, ok := byID["tasks[2]|"+string(quarantined.ID())]; !ok || !n.Skipped {
		t.Fatalf("expected the quarantined device to be lifecycle-skipped, got %+v", n)
	} else {
		assertSpanned(t, n, before, after)
	}
}

// TestNodeResultBoundsAFailedNode confirms the failure returns bound their
// result too. A run that ends in a failure is the one the journal is read
// for afterward, so losing the duration there loses it exactly where it is
// wanted.
func TestNodeResultBoundsAFailedNode(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "spans-failure",
		"tasks": [{"name": "doomed", "fqcn": "noop"}]
	}`)

	x := engine.NewExecutor(mapResolver{}, failingActionExecutor{}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	before := time.Now().UTC()
	result, err := x.Run(context.Background(), dag)
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("Run itself must not error on a node failure, got %v", err)
	}
	if len(result.Nodes) != 1 {
		t.Fatalf("expected 1 node result, got %d: %+v", len(result.Nodes), result.Nodes)
	}
	if result.Nodes[0].Err == nil {
		t.Fatalf("expected the node to fail, got %+v", result.Nodes[0])
	}
	assertSpanned(t, result.Nodes[0], before, after)
}

// TestConditionResultCarriesOrdinalAndTotal compiles each of the three
// condition keywords through the real Conditional.Compile and asserts the
// numbers against the sentence they were taken from. The Reason
// assertions are deliberately exact substrings of the existing wording:
// this widening must not change that text.
func TestConditionResultCarriesOrdinalAndTotal(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}

	// The activation every condition is evaluated against in runNode.
	vars := map[string]interface{}{
		"stat":  map[string]interface{}{},
		"nodes": map[string]interface{}{},
		"vars":  map[string]interface{}{},
	}

	cases := []struct {
		name        string
		cond        engine.Conditional
		wantOK      bool
		wantOrdinal int
		wantTotal   int
		wantReason  string
	}{
		{
			name:        "when list, second item is the one that fails",
			cond:        engine.Conditional{When: engine.StringList{"true", "false", "true"}},
			wantOrdinal: 2,
			wantTotal:   3,
			wantReason:  "when condition 2 of 3 evaluated false",
		},
		{
			name:        "single when item is condition 1 of 1 even though the sentence omits the numbers",
			cond:        engine.Conditional{When: engine.StringList{"false"}},
			wantOrdinal: 1,
			wantTotal:   1,
			wantReason:  "when `false` evaluated false",
		},
		{
			name:        "when_cel is always the single-item case",
			cond:        engine.Conditional{WhenCEL: "false"},
			wantOrdinal: 1,
			wantTotal:   1,
			wantReason:  "when_cel `false` evaluated false",
		},
		{
			name:        "when_or reports the total and no ordinal, because every item contributed",
			cond:        engine.Conditional{WhenOr: engine.StringList{"false", "false"}},
			wantOrdinal: 0,
			wantTotal:   2,
			wantReason:  "when_or: all 2 conditions evaluated false",
		},
		{
			name:       "a condition that holds carries neither number",
			cond:       engine.Conditional{When: engine.StringList{"true", "true"}},
			wantOK:     true,
			wantReason: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := tc.cond.Compile(eval)
			if err != nil {
				t.Fatalf("Compile failed: %v", err)
			}
			res, err := cp.Eval(vars)
			if err != nil {
				t.Fatalf("Eval failed: %v", err)
			}
			if res.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (reason %q)", res.OK, tc.wantOK, res.Reason)
			}
			if res.Ordinal != tc.wantOrdinal {
				t.Errorf("Ordinal = %d, want %d", res.Ordinal, tc.wantOrdinal)
			}
			if res.Total != tc.wantTotal {
				t.Errorf("Total = %d, want %d", res.Total, tc.wantTotal)
			}
			if tc.wantReason == "" {
				if res.Reason != "" {
					t.Errorf("Reason = %q, want empty", res.Reason)
				}
				return
			}
			if !strings.Contains(res.Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want it to contain %q: the numbers were added beside this sentence, not in place of it", res.Reason, tc.wantReason)
			}
		})
	}
}
