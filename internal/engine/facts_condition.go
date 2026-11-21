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
	"time"
)

// errNoFactDevice is why a task with no device reads no fact.
var errNoFactDevice = errors.New("the task has no device")

// factsWithheldError is what replaces CEL's message for a condition that read
// a stored value and then failed to evaluate.
const factsWithheldError = "evaluating it failed on a stored fact's value; CEL's own message is not shown, since it can quote the value"

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
		return "no method that emits it has observed this device, in this run or a run before it: gather it in an earlier task of this run (pleiades doc <method> lists the facts a method emits), or run a gather first"
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
// root, with nothing in it CEL took from a value: it names an absent key the
// condition read without testing for it, or withholds CEL's message. An
// error of a condition that read no fact is returned as it is.
func (v *factsView) sanitize(err error) error {
	if err == nil || !v.used() {
		return err
	}
	if len(v.absent) > 0 {
		return fmt.Errorf("it reads %s, which the device does not have; test for it first with has(), \"<key>\" in facts or facts[?\"<key>\"]",
			QuotedFactKeys(uniqueSorted(v.absent)))
	}
	return errors.New(factsWithheldError)
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
