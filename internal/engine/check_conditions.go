// Package engine: a task's condition in a check.
//
// In a check every registered result is either a prediction (the task was
// checked) or missing (it could not be, so it registered nothing). A
// condition reading a missing one does not have an answer, but a condition
// whose other parts settle it does, and a condition that is simply wrong
// fails exactly as the real run would. That is the three-way split this
// file makes, using CEL partial evaluation to find out which parts the
// missing results decide.
package engine

import (
	"fmt"
	"sync"
)

// unknownRegisters records, for one run, every registered result a check
// could not produce. Nodes of a level run concurrently, so it is locked;
// a condition only ever reads results from earlier levels, so the set a
// condition sees is complete for everything it can read.
type unknownRegisters struct {
	mu   sync.Mutex
	list []UnknownRegister
}

// add records one unknown result.
func (u *unknownRegisters) add(r UnknownRegister) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.list = append(u.list, r)
}

// snapshot returns a copy of every unknown result recorded so far.
func (u *unknownRegisters) snapshot() []UnknownRegister {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]UnknownRegister(nil), u.list...)
}

// has reports whether any result registered under name is unknown.
func (u *unknownRegisters) has(name string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, r := range u.list {
		if r.Name == name {
			return true
		}
	}
	return false
}

// markUnchecked records that task, which could not be checked, registered
// nothing: on one device, or, when whole is set, on any device.
func (r *run) markUnchecked(task *Task, device string, whole bool) {
	if task.Register == "" {
		return
	}
	r.unknown.add(UnknownRegister{Name: task.Register, Device: device, Whole: whole})
}

// checkCondition evaluates task's condition in a check. It returns the
// condition's result when the condition is decided; a non-empty undecided
// reason when it is not; or an error when the real run would fail the
// same way.
//
// A condition is undecided in two cases. The first is that its answer
// depends on a result a task could not check (partial evaluation left it
// unknown). The second is an evaluation error while the condition reads a
// predicted result: a prediction need not carry every field a real run's
// result would, so the real run might not fail there. An error that no
// prediction can explain is a failure: the condition reads a result no
// task in this run registers (a misspelled register), or reads none at
// all, and the real run would stop on it too.
func (r *run) checkCondition(task *Task, cp *ConditionProgram, vars map[string]interface{}, tree map[string]interface{}) (ConditionResult, string, error) {
	res, known, err := cp.EvalPartial(vars, r.unknown.snapshot())
	switch {
	case err == nil && known:
		return res, "", nil
	case err == nil:
		return ConditionResult{}, "its condition depends on the result of a task that could not be checked", nil
	}

	names, dynamic, readErr := ConditionReads(task.Conditional)
	if readErr != nil || dynamic {
		return ConditionResult{}, fmt.Sprintf("its condition could not be evaluated against predicted results, and which results it reads cannot be told before it runs: %v", err), nil
	}
	readsPrediction := false
	for _, name := range names {
		switch _, registered := tree[name]; {
		case registered:
			readsPrediction = true
		case r.unknown.has(name):
		default:
			return ConditionResult{}, "", fmt.Errorf("%w (no task before it in this run registers %q, so a real run would fail here too)", err, name)
		}
	}
	if readsPrediction {
		return ConditionResult{}, fmt.Sprintf("its condition could not be evaluated against predicted results, which may not carry every field a real run's result would: %v", err), nil
	}
	return ConditionResult{}, "", err
}
