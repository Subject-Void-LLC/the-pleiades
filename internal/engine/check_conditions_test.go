// Package engine_test: tests of how a check evaluates a condition it cannot
// fully know.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// conditionOutcome is what became of a task whose condition a check
// evaluated.
type conditionOutcome string

const (
	outcomeChecked   conditionOutcome = "checked"   // the condition held and the task was checked
	outcomeSkipped   conditionOutcome = "skipped"   // the condition was false, as it would be in a real run
	outcomeUnchecked conditionOutcome = "unchecked" // the condition's answer depends on what a check cannot know
	outcomeFailed    conditionOutcome = "failed"    // the condition is wrong, and a real run would fail on it too
)

// checkConditionCase runs, as a check, a runbook of three tasks: one that
// cannot be checked registering a, one that can registering b (a
// prediction whose one field is seen: "current-state"), and a checkable
// consumer carrying condition (a JSON fragment: when, when_or or
// when_cel). Extra variables force (true) and never (false) are set. It
// returns what became of the consumer and its result.
func checkConditionCase(t *testing.T, condition string) (conditionOutcome, engine.NodeResult) {
	t.Helper()
	cannot := registerCheckedMethod(t, "cond-cannot", false, false, false)
	can := registerCheckedMethod(t, "cond-can", true, false, false)
	consumer := registerCheckedMethod(t, "cond-consumer", true, true, false)
	dag := buildDAG(t, `{"id": "check-conditions", "tasks": [
		{"name": "produce-a", "fqcn": "`+cannot.name+`", "register": "a"},
		{"name": "produce-b", "fqcn": "`+can.name+`", "register": "b"},
		{"name": "consume", "fqcn": "`+consumer.name+`", `+condition+`}
	]}`)

	result, err := newCheckExecutor(mapResolver{},
		engine.WithMode(collection.ModeCheck),
		engine.WithVariables(map[string]interface{}{"force": true, "never": false}),
	).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	node := nodeByID(t, result, "tasks[2]")
	switch {
	case node.Err != nil:
		return outcomeFailed, node
	case node.Unchecked:
		if consumer.checks.Load() != 0 {
			t.Fatalf("an unchecked task's Check ran: %+v", node)
		}
		return outcomeUnchecked, node
	case node.Skipped:
		return outcomeSkipped, node
	default:
		if consumer.checks.Load() != 1 {
			t.Fatalf("the consumer was reported checked but its Check ran %d time(s)", consumer.checks.Load())
		}
		return outcomeChecked, node
	}
}

// TestCheckConditions_PartialEvaluation covers the decision's eight edge
// cases on the real CEL evaluator, compiled through the engine's own
// builder: a condition the unchecked task's missing result decides is
// unchecked, one the rest of the condition settles is answered, and one
// that is wrong fails as the real run would. Each pair of cases differing
// in one known value is the control for the other: the answer moves with
// the value, so the test fails if the unknown were ignored or guessed.
func TestCheckConditions_PartialEvaluation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition string
		want      conditionOutcome
		reason    string
	}{
		// 1. Reading only the unknown result.
		{"1: a condition reading an unchecked task's result", `"when": "stat.a[\"\"].changed"`, outcomeUnchecked, "depends on the result of a task that could not be checked"},

		// 2. An OR a known true member settles, with its control.
		{"2: when_or settled by a true variable", `"when_or": ["stat.a[\"\"].changed", "vars.force"]`, outcomeChecked, ""},
		{"2, control: when_or left open by a false variable", `"when_or": ["stat.a[\"\"].changed", "vars.never"]`, outcomeUnchecked, "depends on"},
		{"2: the same inside one expression", `"when_cel": "stat.a[\"\"].changed || vars.force"`, outcomeChecked, ""},

		// 3. An AND a known false member settles, in either order.
		{"3: an AND list with a false member after the unknown", `"when": ["stat.a[\"\"].changed", "vars.never"]`, outcomeSkipped, "condition 2 of 2 evaluated false"},
		{"3: an AND list with a false member before the unknown", `"when": ["vars.never", "stat.a[\"\"].changed"]`, outcomeSkipped, "condition 1 of 2 evaluated false"},
		{"3, control: an AND list whose known member is true", `"when": ["vars.force", "stat.a[\"\"].changed"]`, outcomeUnchecked, "depends on"},

		// 4. A misspelled register fails, as the real run would.
		{"4: a misspelled register", `"when_cel": "stat.reslt[\"\"].x == 1"`, outcomeFailed, `registers "reslt"`},
		{"4: a misspelled register in a when list", `"when": ["stat.reslt[\"\"].x == 1"]`, outcomeFailed, `registers "reslt"`},

		// 5. A misspelling beside an unknown: the real run's outcome
		// depends on the unknown, so unchecked is the honest answer.
		{"5: a misspelling ORed with an unknown", `"when_cel": "stat.reslt[\"\"].x == 1 || stat.a[\"\"].changed"`, outcomeUnchecked, "depends on"},
		{"5: a misspelling after an unknown in a when list", `"when": ["stat.a[\"\"].changed", "stat.reslt[\"\"].x == 1"]`, outcomeUnchecked, "depends on"},
		{"5, control: a misspelling before an unknown in a when list", `"when": ["stat.reslt[\"\"].x == 1", "stat.a[\"\"].changed"]`, outcomeFailed, `registers "reslt"`},

		// 6. A checked task's predicted result is known.
		{"6: a predicted result that holds", `"when_cel": "stat.b[\"\"].seen == 'current-state'"`, outcomeChecked, ""},
		{"6, control: a predicted result that does not", `"when_cel": "stat.b[\"\"].seen == 'other'"`, outcomeSkipped, "evaluated false"},

		// 7. Presence of the unknown result is itself unknown.
		{"7: has() of an unchecked task's result", `"when_cel": "has(stat.a)"`, outcomeUnchecked, "depends on"},
		{"7, control: has() of a predicted result", `"when_cel": "has(stat.b)"`, outcomeChecked, ""},

		// 8. The nodes variable reaches the same results.
		{"8: nodes. reading an unchecked task's result", `"when_cel": "nodes.a[\"\"].changed"`, outcomeUnchecked, "depends on"},
		{"8, control: nodes. reading a predicted result", `"when_cel": "nodes.b[\"\"].seen == 'current-state'"`, outcomeChecked, ""},

		// Beyond the eight: a field a prediction does not carry is not
		// proof the real run fails, and an error reading no result is.
		{"a field the prediction does not carry", `"when_cel": "stat.b[\"\"].rc == 0"`, outcomeUnchecked, "may not carry every field"},
		{"a misspelled variable, reading no result", `"when_cel": "vars.forse"`, outcomeFailed, "forse"},
		{"a condition reading results dynamically", `"when_cel": "stat[vars.force ? 'b' : 'a'][\"\"].rc == 0"`, outcomeUnchecked, "cannot be told before it runs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, node := checkConditionCase(t, tc.condition)
			if got != tc.want {
				t.Fatalf("outcome = %s, want %s: %+v", got, tc.want, node)
			}
			text := node.SkipReason
			if node.Err != nil {
				text = node.Err.Error()
			}
			if !strings.Contains(text, tc.reason) {
				t.Errorf("the %s task says %q, want it to contain %q", got, text, tc.reason)
			}
		})
	}
}

// TestCheckConditions_AnUndecidedTaskIsUnknownToo covers the chain: a task
// whose own condition was undecided registered nothing, so a task reading
// its result is undecided in turn, rather than failing on a register that
// seems never to have been written.
func TestCheckConditions_AnUndecidedTaskIsUnknownToo(t *testing.T) {
	cannot := registerCheckedMethod(t, "chain-cannot", false, false, false)
	middle := registerCheckedMethod(t, "chain-middle", true, true, false)
	last := registerCheckedMethod(t, "chain-last", true, true, false)
	dag := buildDAG(t, `{"id": "check-chain", "tasks": [
		{"name": "produce", "fqcn": "`+cannot.name+`", "register": "a"},
		{"name": "middle", "fqcn": "`+middle.name+`", "register": "m", "when_cel": "stat.a[\"\"].changed"},
		{"name": "last", "fqcn": "`+last.name+`", "when_cel": "stat.m[\"\"].seen == 'current-state'"}
	]}`)
	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"tasks[1]", "tasks[2]"} {
		if n := nodeByID(t, result, id); !n.Unchecked || n.Err != nil {
			t.Errorf("%s = %+v, want it unchecked", id, n)
		}
	}
}

// TestCheckConditions_UnknownIsPerDevice covers the device key: a task
// that could not be checked on its two devices leaves each device's
// result unknown, a condition reading one of them is undecided, and one
// reading a device the task never ran on fails, as the real run would,
// where that device's key would be missing too.
func TestCheckConditions_UnknownIsPerDevice(t *testing.T) {
	for _, tc := range []struct {
		device string
		want   conditionOutcome
	}{{"dev-1", outcomeUnchecked}, {"dev-9", outcomeFailed}} {
		t.Run(tc.device, func(t *testing.T) {
			cannot := registerCheckedMethod(t, "device-cannot", false, false, false)
			consumer := registerCheckedMethod(t, "device-consumer", true, true, false)
			fleet := []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "dev-1", StubName: "dev-1", StubState: inventory.StateActive},
				&inventorytest.Stub{StubID: "dev-2", StubName: "dev-2", StubState: inventory.StateActive},
			}
			dag := buildDAG(t, `{"id": "check-devices", "tasks": [
				{"name": "produce", "fqcn": "`+cannot.name+`", "register": "a", "params": {"target": "fleet"}},
				{"name": "consume", "fqcn": "`+consumer.name+`", "when_cel": "stat.a[\"`+tc.device+`\"].changed"}
			]}`)
			result, err := newCheckExecutor(mapResolver{"fleet": fleet}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			n := nodeByID(t, result, "tasks[1]")
			got := outcomeUnchecked
			switch {
			case n.Err != nil:
				got = outcomeFailed
			case !n.Unchecked:
				got = outcomeChecked
			}
			if got != tc.want {
				t.Errorf("reading %s's result: %s, want %s: %+v", tc.device, got, tc.want, n)
			}
		})
	}
}
