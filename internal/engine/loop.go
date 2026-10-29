// Loops and retries (Phases 88 and 89): a task that runs once per item of a
// list (loop:, or Ansible's older with_items:) and a task that runs again
// until a condition holds (until:, retries:, delay:).
//
// Both are decided at run time, per device, inside one DAG node, never
// expanded into sibling nodes at build time: a list read from a registered
// result does not exist until an earlier task has run. A task that loops or
// retries therefore runs on each device on its own, as one reading the device
// root does (readsDevice).
//
// Every run of the method is its own result and its own journal entry
// (NodeResult.Iteration), so a rollback undoes the items and the tries one by
// one, newest first. The register is the exception, and is written once by the
// driver: a loop registers {results: [...], changed, failed, skipped} with one
// element per item, and an until task registers the last try only.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The bounds on a loop and a retry, and the defaults an until task gets from
// Ansible's own.
const (
	// MaxLoopItems bounds how many items a loop may run, so a list read from
	// a device's output cannot make a task run without end.
	MaxLoopItems = 1000
	// MaxRetries bounds retries: it is how many more tries an until task gets.
	MaxRetries = 100
	// MaxDelaySeconds bounds delay, the seconds between tries.
	MaxDelaySeconds = 3600
	// MaxTotalRuns bounds the runs of one task on one device: its items times
	// the tries each may take.
	MaxTotalRuns = 5000
	// MaxLoopResultBytes bounds what a loop's registered results may hold, as
	// JSON, so a thousand large outputs cannot fill the run's memory.
	MaxLoopResultBytes = 16 << 20
	// DefaultRetries and DefaultDelaySeconds apply to an until task that sets
	// neither.
	DefaultRetries      = 3
	DefaultDelaySeconds = 5
)

// LoopControl is a task's loop_control: the names its loop uses and the label
// its items are known by.
type LoopControl struct {
	// LoopVar is the name an item is read under, "item" when empty.
	LoopVar string `json:"loop_var,omitempty" yaml:"loop_var,omitempty"`
	// IndexVar, when set, is a name the item's position (from 0) is also read
	// under.
	IndexVar string `json:"index_var,omitempty" yaml:"index_var,omitempty"`
	// Label is a template rendered for each item and kept with its result, so
	// a report says which item a result is without printing the item itself.
	Label string `json:"label,omitempty" yaml:"label,omitempty"`
}

// taskPrograms are the programs compiled from a task's own keys.
type taskPrograms struct {
	// until, changed and failed are the compiled until, changed_when and
	// failed_when conditions, nil for a key the task does not set; assert is
	// the compiled that: list of an assert task.
	until, changed, failed, assert *ConditionProgram
}

// compileTaskPrograms compiles the conditions task's own keys hold, or
// returns nil for a task with none.
func compileTaskPrograms(eval Evaluator, task *Task) (*taskPrograms, error) {
	p := &taskPrograms{}
	for _, c := range []struct {
		name string
		list StringList
		into **ConditionProgram
	}{
		{"until", task.Until, &p.until},
		{"changed_when", task.ChangedWhen, &p.changed},
		{"failed_when", task.FailedWhen, &p.failed},
		{"that", assertThat(task), &p.assert},
	} {
		if len(c.list) == 0 {
			continue
		}
		prg, err := Conditional{When: c.list}.Compile(eval)
		if err != nil {
			return nil, fmt.Errorf("failed to compile %s: %w", c.name, err)
		}
		*c.into = prg
	}
	if *p == (taskPrograms{}) {
		return nil, nil
	}
	return p, nil
}

// loopSource is the task's loop: or with_items: value, nil when it has neither.
func (t *Task) loopSource() interface{} {
	if t.Loop != nil {
		return t.Loop
	}
	return t.WithItems
}

// Iterates reports whether the task runs more than once on a device: it
// loops, or retries with until.
func (t *Task) Iterates() bool { return t.iterates() }

// iterates reports whether the task runs more than once on a device.
func (t *Task) iterates() bool { return t.loopSource() != nil || len(t.Until) > 0 }

// loopVar is the name the task's items are read under.
func (t *Task) loopVar() string {
	if t.LoopControl != nil && t.LoopControl.LoopVar != "" {
		return t.LoopControl.LoopVar
	}
	return "item"
}

// loopRoots are the extra roots a looped task's templates and conditions may
// read: its item, the position, and the names loop_control gave them. A task
// with no loop has none.
func (t *Task) loopRoots() []string {
	if t.loopSource() == nil {
		return nil
	}
	roots := []string{"item", "loop"}
	if t.LoopControl != nil {
		for _, n := range []string{t.LoopControl.LoopVar, t.LoopControl.IndexVar} {
			if n != "" && !slices.Contains(roots, n) {
				roots = append(roots, n)
			}
		}
	}
	return roots
}

// authoredLoopRoot reports whether root is one of the task's loop roots and
// the loop's list comes from the author: written out in the runbook with no
// template in it, or one expression reading only vars. Such an item is the
// author's, so it may choose a project file; an item read from a device's
// output may not (render_params.go's project-path rule).
func (t *Task) authoredLoopRoot(root string) bool {
	if !slices.Contains(t.loopRoots(), root) {
		return false
	}
	switch src := t.loopSource().(type) {
	case []any:
		return !HasTemplate(src)
	case string:
		tmpl, err := render.New().Compile(src)
		if err != nil {
			return false
		}
		for _, e := range tmpl.Expressions() {
			if len(e.Path) == 0 || e.Path[0] != TemplateRootVars {
				return false
			}
		}
		return true
	}
	return false
}

// triesAllowed is how many times an until task may run: its first run and its
// retries.
func (t *Task) triesAllowed() int {
	retries := DefaultRetries
	if t.Retries != nil {
		retries = *t.Retries
	}
	return 1 + retries
}

// triesAllowedFor is triesAllowed in mode: a check never retries.
func (t *Task) triesAllowedFor(mode collection.Mode) int {
	if len(t.Until) == 0 || mode == collection.ModeCheck {
		return 1
	}
	return t.triesAllowed()
}

// delay is the wait between an until task's tries.
func (t *Task) delay() time.Duration {
	d := DefaultDelaySeconds
	if t.Delay != nil {
		d = *t.Delay
	}
	return time.Duration(d) * time.Second
}

// runTask prepares and dispatches task on devices (the one device key names,
// or none), once, or once per item, or once per try, as the task says.
func (r *run) runTask(ctx context.Context, nodeID string, task *Task, started time.Time, key string, devices []inventory.InventoryItem, roots map[string]any) []NodeResult {
	if task.progs != nil && (task.progs.changed != nil || task.progs.failed != nil || task.progs.assert != nil) && !task.iterates() {
		// The classification and the assertion read what the task was decided
		// with, so the copy that runs keeps it.
		c := *task
		c.evalRoots = roots
		task = &c
	}
	if !task.iterates() {
		prepared, early := r.prepare(nodeID, task, started, key, roots)
		if early != nil {
			return []NodeResult{*early}
		}
		return r.dispatch(ctx, nodeID, prepared, devices, started)
	}
	if task.loopSource() == nil {
		out := r.tries(ctx, nodeID, task, started, key, devices, roots, "", "")
		r.registerFinal(nodeID, task, key, out)
		return out.results
	}
	return r.loop(ctx, nodeID, task, started, key, devices, roots)
}

// outcome is what one item (or a task with no loop) came to after its tries.
type outcome struct {
	results []NodeResult
	// stats are the last try's, empty when it did not run.
	stats                    map[string]any
	changed, failed, skipped bool
	// methodRan says the last try's method finished without failing, so its
	// result is one to register even when until never held.
	methodRan bool
}

// loop runs task once for each of its items, in order, whatever one of them
// does: an item that fails does not stop the rest, and the task fails if any
// did (Ansible's rule).
func (r *run) loop(ctx context.Context, nodeID string, task *Task, started time.Time, key string, devices []inventory.InventoryItem, roots map[string]any) []NodeResult {
	items, err := r.loopItems(task, roots)
	if err != nil {
		wrapped := fmt.Errorf("failed to resolve the loop of %s: %w", taskLabel(nodeID, task), err)
		r.publish(nodeID, task, "", "failed", wrapped.Error())
		failed := failedNode(nodeID, started, FailureStageRender, wrapped)
		failed.Device = key
		return []NodeResult{failed}
	}
	if total := len(items) * task.triesAllowedFor(r.modeFor(task)); total > MaxTotalRuns {
		wrapped := fmt.Errorf("%s would run %d times on one device (%d items with up to %d tries each), and a task runs at most %d", taskLabel(nodeID, task), total, len(items), task.triesAllowedFor(r.modeFor(task)), MaxTotalRuns)
		r.publish(nodeID, task, "", "failed", wrapped.Error())
		failed := failedNode(nodeID, started, FailureStageRender, wrapped)
		failed.Device = key
		return []NodeResult{failed}
	}
	var results []NodeResult
	var rows []any
	anyChanged, anyFailed, allSkipped := false, false, len(items) > 0
	resultBytes := 0
	for k, item := range items {
		if ctx.Err() != nil {
			break
		}
		itemRoots := make(map[string]any, len(roots)+4)
		for name, v := range roots {
			itemRoots[name] = v
		}
		itemRoots[task.loopVar()] = item
		// item is always readable by a condition, whatever the loop_var is
		// called (cel.go declares item and not the author's name).
		itemRoots["item"] = item
		itemRoots["loop"] = map[string]any{
			"index": k + 1, "index0": k, "first": k == 0, "last": k == len(items)-1, "length": len(items),
		}
		if task.LoopControl != nil && task.LoopControl.IndexVar != "" {
			itemRoots[task.LoopControl.IndexVar] = k
		}
		label := ""
		if task.LoopControl != nil && task.LoopControl.Label != "" {
			if label, err = r.itemLabel(task, itemRoots); err != nil {
				wrapped := fmt.Errorf("failed to render the label of item %d of %s: %w", k, taskLabel(nodeID, task), err)
				r.publish(nodeID, task, "", "failed", wrapped.Error())
				failed := failedNode(nodeID, started, FailureStageRender, wrapped)
				failed.Device, failed.Iteration = key, fmt.Sprintf(".item[%d]", k)
				results = append(results, failed)
				anyFailed = true
				continue
			}
		}
		out := r.tries(ctx, nodeID, task, started, key, devices, itemRoots, fmt.Sprintf(".item[%d]", k), label)
		results = append(results, out.results...)
		row := map[string]any{"item": item}
		for name, v := range out.stats {
			row[name] = v
		}
		row["changed"], row["failed"], row["skipped"] = out.changed, out.failed, out.skipped
		rows = append(rows, row)
		if encoded, err := json.Marshal(row); err == nil {
			if resultBytes += len(encoded); resultBytes > MaxLoopResultBytes {
				wrapped := fmt.Errorf("the results of %s hold more than %d bytes after item %d; a loop keeps every item's result, so loop over fewer items or return less from the method", taskLabel(nodeID, task), MaxLoopResultBytes, k)
				r.publish(nodeID, task, "", "failed", wrapped.Error())
				failed := failedNode(nodeID, started, FailureStageRecord, wrapped)
				failed.Device, failed.Iteration = key, fmt.Sprintf(".item[%d]", k)
				results = append(results, failed)
				anyFailed = true
				break
			}
		}
		anyChanged = anyChanged || out.changed
		anyFailed = anyFailed || out.failed
		allSkipped = allSkipped && out.skipped
	}
	if task.Register != "" {
		aggregate := map[string]any{"results": rows, "changed": anyChanged, "failed": anyFailed, "skipped": allSkipped}
		if rows == nil {
			aggregate["results"] = []any{}
		}
		if err := r.x.workflow.Merge(task.Register, key, aggregate); err != nil {
			wrapped := fmt.Errorf("failed to record result of task %s: %w", taskLabel(nodeID, task), err)
			r.publish(nodeID, task, "", "failed", wrapped.Error())
			failed := failedNode(nodeID, started, FailureStageRecord, wrapped)
			failed.Device = key
			results = append(results, failed)
		}
	}
	return results
}

// evalContext is the template context a task evaluates in with roots added.
func (r *run) evalContext(task *Task, roots map[string]any) (map[string]any, error) {
	ctx, err := r.templateContext()
	if err != nil {
		return nil, err
	}
	vars, err := r.mergedVars(task, roots)
	if err != nil {
		return nil, err
	}
	ctx[TemplateRootVars] = vars
	for k, v := range roots {
		if k != hostVarsRoot {
			ctx[k] = v
		}
	}
	return ctx, nil
}

// loopItems resolves the task's loop to its list of items: a list written in
// the runbook, each element rendered, or one expression rendering to a list.
func (r *run) loopItems(task *Task, roots map[string]any) ([]any, error) {
	var items []any
	switch src := task.loopSource().(type) {
	case []any:
		items = src
		if HasTemplate(src) {
			ctx, err := r.rendererContext(task, roots)
			if err != nil {
				return nil, err
			}
			rendered, err := renderValue(r.x.renderer, src, ctx)
			if err != nil {
				return nil, err
			}
			items = rendered.([]any)
		}
	case string:
		if !HasTemplate(src) {
			return nil, fmt.Errorf("a loop given as text must be one {{ }} expression that renders to a list, not %q", src)
		}
		ctx, err := r.rendererContext(task, roots)
		if err != nil {
			return nil, err
		}
		rendered, err := renderValue(r.x.renderer, src, ctx)
		if err != nil {
			return nil, err
		}
		v := reflect.ValueOf(rendered)
		if rendered == nil || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) {
			return nil, fmt.Errorf("the loop rendered to %s, and a loop needs a list", collection.ValueKind(rendered))
		}
		for i := 0; i < v.Len(); i++ {
			items = append(items, v.Index(i).Interface())
		}
	default:
		return nil, fmt.Errorf("a loop is a list or one {{ }} expression, not %s", collection.ValueKind(src))
	}
	if len(items) > MaxLoopItems {
		return nil, fmt.Errorf("the loop has %d items, and a loop runs at most %d", len(items), MaxLoopItems)
	}
	return items, nil
}

// rendererContext is evalContext after checking the run has a renderer.
func (r *run) rendererContext(task *Task, roots map[string]any) (map[string]any, error) {
	if r.x.renderer == nil {
		return nil, fmt.Errorf("this run has no template renderer, so a loop holding {{ cannot be resolved")
	}
	return r.evalContext(task, roots)
}

// itemLabel renders the task's loop_control label for the item in roots.
func (r *run) itemLabel(task *Task, roots map[string]any) (string, error) {
	ctx, err := r.rendererContext(task, roots)
	if err != nil {
		return "", err
	}
	text, err := renderText(r.x.renderer, task.LoopControl.Label, ctx)
	if err != nil {
		return "", err
	}
	s, _ := text.(string)
	return s, nil
}

// iterationSuffix matches the runs a journal entry's node id may end in, in
// the order they are written: an item, then a try of it.
var iterationSuffix = regexp.MustCompile(`(\.(item|attempt)\[\d+\])+`)

// BaseNodeID returns id, a journal entry's node id, without the item and try
// it names, which is the id of the task in the DAG. A step of a block's
// rollback_on_failure (".undo[k]") is not a task of the DAG and keeps its
// whole id, as does an id that names no iteration.
func BaseNodeID(id string) string {
	if strings.HasSuffix(id, "]") && strings.Contains(id, ".undo[") {
		return id
	}
	return iterationSuffix.ReplaceAllString(id, "")
}

// NodeFor returns the task a journal entry's node id names: the DAG's own
// node, whichever item or try of it the id says.
func (d *DAG) NodeFor(id string) (*Task, bool) {
	t, ok := d.Nodes[BaseNodeID(id)]
	return t, ok
}
