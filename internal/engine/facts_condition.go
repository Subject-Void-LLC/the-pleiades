// Package engine: what a condition reading the facts root comes to, after
// CEL has evaluated it (facts_view.go).
//
// A condition is three-valued. A key with no answer is a CEL unknown, CEL
// decides what it can without it, and the task fails only when the result
// itself is unknown, naming the keys it depended on, why each has no answer
// and which methods would gather it; a result that is known stands, whatever
// keys it did not need. In a check, a key an earlier checked task would emit
// is unknown by the same rule, and a result that depends on one is a gap in
// the check rather than a failure. An evaluation error of a condition that
// read a stored value withholds CEL's message, which can quote the value: a
// fact is device-supplied text, and the only thing about it a run may show is
// whether the condition held.
package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// errNoFactDevice is why a task with no device reads no fact.
var errNoFactDevice = errors.New("the task has no device")

// factsWithheldError is what replaces CEL's message for a condition that read
// a stored value and then failed to evaluate.
const factsWithheldError = "CEL's own message is not shown, since the condition read a stored fact and the message could quote its value; the cause may be any part of the expression, a fact or another root"

// factsUndecided is the undecided reason of a checked task whose condition
// read a stored fact, in place of one quoting CEL's message.
const factsUndecided = "its condition could not be evaluated against predicted results, and it reads a stored fact, so CEL's message is not shown"

// failure is the error that fails the evaluation: the root used as a whole,
// a read of a key resolve did not answer (never expected), or a result left
// unknown by a key that has no answer. deps is what an unknown result
// depended on; a known result passes an empty one. A result whose only
// unknowns are keys a check cannot know, or registers, fails nothing here
// (checkGap). Nil leaves the condition's own answer standing.
func (v *factsView) failure(nodeID string, task *Task, deviceName string, deps unknownDeps) error {
	label := taskLabel(nodeID, task)
	if v.whole {
		return fmt.Errorf("task %s used the facts root as a value, which is refused: %s; the task did not run", label, factsAllowed)
	}
	if keys := v.missedKeys(); len(keys) > 0 {
		return fmt.Errorf("task %s reads %s on device %s, which has no stored answer for it: %s; the task did not run",
			label, QuotedFactKeys(keys), deviceName, missReason(v.misses[0].err))
	}
	var keys []string
	for _, key := range v.dependedOn(deps) {
		if _, unknown := v.unknown[key]; unknown {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 || (v.checked && (deps.other || v.anyTainted(deps))) {
		return nil
	}
	return fmt.Errorf("task %s reads %s on device %s, and its condition cannot be decided without it: %s; %s; the task did not run",
		label, QuotedFactKeys(keys), deviceName, missReason(v.unknown[keys[0]]), howToGather(keys))
}

// checkGap is why a checked task's unknown result is a gap in the check and
// not its failure: it depended on a key an earlier checked task would emit,
// or on a register an unchecked task never wrote, either of which a real run
// would know. "" when the result is known or must fail (failure).
func (v *factsView) checkGap(deps unknownDeps) string {
	for _, key := range v.dependedOn(deps) {
		if reason := v.tainted[key]; reason != "" {
			return reason
		}
	}
	return ""
}

// anyTainted reports whether deps reached a key a check cannot know.
func (v *factsView) anyTainted(deps unknownDeps) bool {
	return v.checkGap(deps) != ""
}

// dependedOn is the fact keys an unknown result depended on: those deps
// names, or every unknown and tainted key when it reached the root whole.
func (v *factsView) dependedOn(deps unknownDeps) []string {
	if !deps.allFacts {
		return deps.facts
	}
	var keys []string
	for _, u := range v.unknowns() {
		keys = append(keys, u.Name)
	}
	return keys
}

// howToGather names the methods that would gather keys' first key: those
// declaring it by name, and those listing the family it belongs to, which a
// parameter of theirs may choose (pleiades doc says which). It names no value.
func howToGather(keys []string) string {
	byName, byFamily := collection.FactDeclarers(keys[0])
	switch {
	case len(byName) > 0:
		return fmt.Sprintf("gather it in an earlier task of this run, with nothing narrowing the call, through %s (pleiades doc <method> lists what each emits)", strings.Join(byName, " or "))
	case len(byFamily) > 0:
		return fmt.Sprintf("it is an item of a family, which %s lists only where the device has it (pleiades doc <method> says which parameter gathers the family), so a stored fact can never say it is not there: register that task and test its result instead", strings.Join(byFamily, " or "))
	}
	return "no method this binary knows declares it, so no gather can answer it"
}

// missReason says why a read had no answer.
func missReason(err error) string {
	switch {
	case err == nil:
		return "nothing this run or the store holds says whether the device has it: it was never gathered, a read that could not tell left it out, or a family's other items were listed without it, and the store keeps what a gather found, not what it looked for"
	case errors.Is(err, ErrNoFactReader):
		return "this run has no store of facts to read (a Runner cannot read stored facts in this release); gather it in an earlier task of the same run and read that task's registered result instead (register:, then result.<name>[\"<key>\"])"
	case errors.Is(err, errNoFactDevice):
		return "the task has no device, so it has no facts; give it hosts: or a target"
	case errors.Is(err, errFactWithheld):
		return err.Error()
	}
	return "the device's stored facts could not be read: " + err.Error()
}

// sanitize is err, an evaluation error of a condition that read the facts
// root, with nothing in it CEL took from a value. It keeps which item of the
// condition failed (its keyword, ordinal and text, all runbook text) and
// drops CEL's own message, which can quote a fact's value. Only when CEL's
// error is the read of an absent fact itself does it name that fact and say
// how to test for it; otherwise it says the cause may be anywhere in the
// expression, since a fact read and a mistake elsewhere look alike once the
// message is gone. An error of a condition that read no fact is returned as
// it is.
func (v *factsView) sanitize(err error) error {
	if err == nil || !v.used() {
		return err
	}
	where := "evaluating it failed"
	var item *conditionItemError
	if errors.As(err, &item) {
		where = fmt.Sprintf("failed to evaluate %s expression %d (`%s`)", item.keyword, item.ordinal, item.expr)
	}
	for _, key := range uniqueSorted(v.absent) {
		// CEL's missing-key error names the key it was asked for, which the
		// condition wrote; a value is never in it.
		if strings.Contains(err.Error(), "no such key: "+key) {
			return fmt.Errorf("%s: it reads %s, which the device does not have; test for it first with has(), \"<key>\" in facts or facts[?\"<key>\"].hasValue()",
				where, QuotedFactKeys([]string{key}))
		}
	}
	return fmt.Errorf("%s: %s", where, factsWithheldError)
}

// uncheckedOn is the result of a checked task the check cannot answer on
// device, for reason: nothing ran, nothing is registered for it, and the
// check is incomplete.
func (r *run) uncheckedOn(nodeID string, task *Task, started time.Time, device, reason string) *NodeResult {
	r.markUnchecked(task, device, device == "")
	r.publish(nodeID, task, "", "skipped", "could not check: "+reason)
	skipped := finish(NodeResult{NodeID: nodeID, Device: device, StartedAt: started, Skipped: true, Unchecked: true, Checked: true, SkipReason: reason})
	return &skipped
}

// uniqueSorted is keys sorted, each once.
func uniqueSorted(keys []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// deviceNameOf is the device a per-device evaluation's roots name, for a
// message: its name, or key when the roots carry none.
func deviceNameOf(roots map[string]any, key string) string {
	if d, ok := roots[TemplateRootDevice].(map[string]any); ok {
		if name, ok := d["name"].(string); ok && name != "" {
			return fmt.Sprintf("%q", name)
		}
	}
	if key == "" {
		return "(none)"
	}
	return fmt.Sprintf("%q", key)
}
