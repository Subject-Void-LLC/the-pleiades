// Retries (Phase 88): until, retries and delay run a task again until a
// condition holds. Each try is its own result and its own journal entry
// (NodeResult.Iteration), only the last is registered, and a check never
// retries. The same try loop runs each item of a loop (loop.go).
package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// tries runs task on devices once, or, for an until task, again until its
// condition holds or its tries run out. A check runs it once: a check never
// retries. suffix and label are the item's, when it is one.
func (r *run) tries(ctx context.Context, nodeID string, task *Task, started time.Time, key string, devices []inventory.InventoryItem, roots map[string]any, suffix, label string) outcome {
	local := *task
	local.noMerge = true
	local.evalRoots = roots
	untilTask := len(task.Until) > 0
	tries := 1
	if untilTask && r.modeFor(task) != collection.ModeCheck {
		tries = task.triesAllowed()
	}
	var out outcome
	for attempt := 0; attempt < tries; attempt++ {
		sfx := suffix
		if untilTask && r.modeFor(task) != collection.ModeCheck {
			sfx += fmt.Sprintf(".attempt[%d]", attempt)
		}
		prepared, early := r.prepare(nodeID, &local, started, key, roots)
		if early != nil {
			early.Iteration, early.Label = sfx, label
			out.results = append(out.results, *early)
			out.failed, out.skipped = early.Err != nil, early.Err == nil && early.Skipped
			return out
		}
		results := r.dispatch(ctx, nodeID, prepared, devices, started)
		for i := range results {
			results[i].Iteration, results[i].Label = sfx, label
		}
		out.stats, out.failed, out.changed, out.skipped = summarize(results)
		out.methodRan = !out.failed && !out.skipped
		if !untilTask || r.modeFor(task) == collection.ModeCheck {
			out.results = append(out.results, results...)
			return out
		}
		held, err := r.untilHolds(task, key, roots, out)
		if err != nil && out.failed {
			// A try that failed has none of the fields a condition reads, so
			// the condition cannot hold for it: try again, or give up.
			held, err = false, nil
		}
		if err != nil {
			failed := failedNode(nodeID, started, FailureStageConditionEval, fmt.Errorf("failed to evaluate until for %s: %w", taskLabel(nodeID, task), err))
			failed.Device, failed.Iteration, failed.Label = key, sfx, label
			out.results = append(out.results, results...)
			out.results = append(out.results, failed)
			out.failed = true
			return out
		}
		if held {
			out.results = append(out.results, results...)
			return out
		}
		if attempt == tries-1 {
			// Out of tries with the condition never holding: the task fails with
			// the last try's own result.
			if len(results) > 0 && results[len(results)-1].Err == nil {
				results[len(results)-1].fail(FailureStageAction, fmt.Errorf("task %s: until never held after %d tries", taskLabel(nodeID, task), tries))
			}
			out.results = append(out.results, results...)
			out.failed = true
			return out
		}
		// Tried again: a failure of this try does not fail the task, only the
		// last try can, so it is kept as a note and not as the result.
		for i := range results {
			if results[i].Err != nil {
				results[i].RetriedErr = results[i].Err.Error()
				results[i].Err = nil
				results[i].failureStage = ""
			}
		}
		out.results = append(out.results, results...)
		select {
		case <-ctx.Done():
			out.failed = true
			return out
		case <-time.After(task.delay()):
		}
	}
	return out
}

// summarize reads one run's results: the stats of the first that has some,
// and whether any failed, changed, or all were skipped.
func summarize(results []NodeResult) (stats map[string]any, failed, changed, skipped bool) {
	skipped = len(results) > 0
	for _, res := range results {
		if res.Err != nil {
			failed = true
		}
		if res.Changed {
			changed = true
		}
		if !res.Skipped {
			skipped = false
		}
		if stats == nil && res.Stats != nil {
			stats = res.Stats
		}
	}
	if failed {
		msg := ""
		for _, res := range results {
			if res.Err != nil {
				msg = res.Err.Error()
				break
			}
		}
		if stats == nil {
			stats = map[string]any{}
		}
		stats = withFailure(stats, msg)
	}
	return stats, failed, changed, skipped
}

// withFailure returns a copy of stats saying the run failed and why.
func withFailure(stats map[string]any, msg string) map[string]any {
	out := make(map[string]any, len(stats)+2)
	for k, v := range stats {
		out[k] = v
	}
	out["failed"], out["msg"] = true, msg
	return out
}

// untilHolds evaluates the task's until against its own result as it stands
// after the try just run, read as the task's register would be.
func (r *run) untilHolds(task *Task, key string, roots map[string]any, out outcome) (bool, error) {
	stats := out.stats
	if stats == nil {
		stats = map[string]any{}
	}
	return r.holds(task.progs.until, task, key, roots, stats)
}

// holds evaluates prg, a condition of task, with stats standing as the task's
// own registered result on the device key names.
func (r *run) holds(prg *ConditionProgram, task *Task, key string, roots map[string]any, stats map[string]any) (bool, error) {
	tree, err := r.x.workflow.Read()
	if err != nil {
		return false, err
	}
	if task.Register != "" && stats != nil {
		patched := make(map[string]any, len(tree)+1)
		for k, v := range tree {
			patched[k] = v
		}
		byDevice := map[string]any{}
		if old, ok := tree[task.Register].(map[string]any); ok {
			for k, v := range old {
				byDevice[k] = v
			}
		}
		byDevice[key] = stats
		patched[task.Register] = byDevice
		tree = patched
	}
	vars, err := r.mergedVars(task, roots)
	if err != nil {
		return false, err
	}
	condVars := map[string]interface{}{"stat": tree, "nodes": tree, "vars": vars, "result": singleWriters(tree)}
	for k, v := range roots {
		if k != hostVarsRoot {
			condVars[k] = v
		}
	}
	res, err := prg.Eval(condVars)
	if err != nil {
		return false, err
	}
	return res.OK, nil
}

// registerFinal writes the register of a task that retries but does not loop:
// the last try's result, when it did not fail.
func (r *run) registerFinal(nodeID string, task *Task, key string, out outcome) {
	if task.Register == "" || !out.methodRan || out.stats == nil {
		return
	}
	if err := r.x.workflow.Merge(task.Register, key, out.stats); err != nil {
		for i := len(out.results) - 1; i >= 0; i-- {
			if out.results[i].Err == nil {
				out.results[i].fail(FailureStageRecord, fmt.Errorf("failed to record result of task %s: %w", taskLabel(nodeID, task), err))
				return
			}
		}
	}
}
