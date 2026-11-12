// Package engine: the variables a task's authored undo renders with.
//
// A task's rollback: steps run long after the task itself, when a block's
// rollback_on_failure undoes it. By then a later set_fact may have changed a
// variable the steps read, and an undo rendered with the new value would undo
// something the task never did, such as removing another path. So the
// variables a task saw when it ran on a device are kept, and its rollback:
// steps render with exactly those (the set_fact decision: an undo uses the
// forward run's values, never the current ones). The registered results and
// the device root are still read when the step runs, since an undo may need
// what the task itself returned.
package engine

import (
	"maps"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// forwardVarsRoot is the internal root under which an undo step's evaluation
// hands mergedVars the variables its task ran with. Like hostVarsRoot it never
// reaches a template.
const forwardVarsRoot = "\x00forwardvars"

// isInternalRoot reports whether k is one of the roots an evaluation passes
// to mergedVars alone (the device's own vars, an undo's kept vars), which are
// never copied into what a template or a condition reads.
func isInternalRoot(k string) bool { return k == hostVarsRoot || k == forwardVarsRoot }

// forwardVars holds, for each task with rollback: steps that read vars, the
// variables it rendered with on each device (or once, under the empty device,
// when it rendered once for all of them). A level's goroutines write it, so
// it has its own lock.
type forwardVars struct {
	mu   sync.Mutex
	vars map[resultKey]map[string]any
}

// keep records vars as what node rendered with on device.
func (f *forwardVars) keep(node, device string, vars map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.vars == nil {
		f.vars = map[resultKey]map[string]any{}
	}
	f.vars[resultKey{node, device}] = maps.Clone(vars)
}

// of returns what node rendered with on device: its own render there, else
// the one render it had for every device, else false.
func (f *forwardVars) of(node, device string) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.vars[resultKey{node, device}]; ok {
		return v, true
	}
	v, ok := f.vars[resultKey{node, ""}]
	return v, ok
}

// rollbackReadsVars reports whether any of task's rollback: steps reads the
// vars root, in a parameter or a target.
func (r *run) rollbackReadsVars(task *Task) bool {
	if r.x.renderer == nil {
		return false
	}
	for i := range task.Rollback {
		if ParamsReadRoot(r.x.renderer, task.Rollback[i].Params, TemplateRootVars) {
			return true
		}
	}
	return false
}

// keepForwardVars records the variables task rendered with on device, when
// its rollback: steps will read them. A check changes nothing and so is never
// undone, and records nothing.
func (r *run) keepForwardVars(nodeID string, task *Task, device string, roots map[string]any) error {
	if r.modeFor(task) != collection.ModeExecute || !r.rollbackReadsVars(task) {
		return nil
	}
	vars, err := r.mergedVars(task, roots)
	if err != nil {
		return err
	}
	r.forward.keep(nodeID, device, vars)
	return nil
}
