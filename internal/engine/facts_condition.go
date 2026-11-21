// Package engine: what a condition reading the facts root comes to, after
// CEL has evaluated it (facts_view.go).
//
// A read with no answer fails the task, naming the task, the key and what to
// do; the condition's own true or false is not used. An evaluation error of a
// condition that read a stored value says which key was absent when one was,
// and otherwise withholds CEL's message, which can quote the value it failed
// on: a fact is device-supplied text, and the only thing about it a run may
// show is whether the condition held.
package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// errNoFactDevice is why a task with no device reads no fact.
var errNoFactDevice = errors.New("the task has no device")

// factsWithheldError is what replaces CEL's message for a condition that read
// a stored value and then failed to evaluate.
const factsWithheldError = "CEL's own message is not shown, since the condition read a stored fact and the message could quote its value; the cause may be any part of the expression, a fact or another root"

// factsUndecided is the undecided reason of a checked task whose condition
// read a stored fact, in place of one quoting CEL's message.
const factsUndecided = "its condition could not be evaluated against predicted results, and it reads a stored fact, so CEL's message is not shown"

// failure is the error that fails the evaluation: a key read with no answer,
// or the root used as a whole. Nil leaves the condition's own answer standing.
func (v *factsView) failure(nodeID string, task *Task, deviceName string) error {
	label := taskLabel(nodeID, task)
	if v.whole {
		return fmt.Errorf("task %s used the facts root as a value, which is refused: %s; the task did not run", label, factsAllowed)
	}
	v.settle()
	keys := v.missedKeys()
	if len(keys) == 0 {
		return nil
	}
	return fmt.Errorf("task %s reads %s on device %s, which has no stored answer for it: %s; the task did not run",
		label, QuotedFactKeys(keys), deviceName, missReason(v.misses[0].err))
}

// missReason says why a read had no answer, and what to do.
func missReason(err error) string {
	switch {
	case err == nil:
		return "nothing this run or the store holds says whether the device has it: it was never gathered, or a narrowed gather or a family's other items left it out, and the store keeps what a gather found, not what it looked for; gather it in an earlier task of this run, with nothing narrowing the call (pleiades doc <method> lists the facts a method emits)"
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
