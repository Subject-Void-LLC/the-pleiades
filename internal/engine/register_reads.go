// Package engine: which registered results a task reads, from every place a
// task can read one: its condition, its rendered parameters, its secret_mask
// and its authored rollback: steps. The segmenter (internal/segment) asks it;
// validate's rules still each read their own seam (TemplateRule templates,
// ConditionRule conditions, SecretMaskRule secret_mask).
package engine

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// Reads is what one task reads of the registered results.
type Reads struct {
	// Names are the registers the task names, sorted and without repeats.
	Names []string

	// All reports that the task also reaches the registered results in a way
	// no name can be read off (a whole root handed to a filter or a macro,
	// an index computed as the run goes), so it may read any register and a
	// caller must assume it reads every one.
	All bool
}

// TemplateReads returns the registers one compiled template names, sorted:
// every expression reading nodes.<name> or result.<name>. all reports an
// expression that reads nodes or result whole ({{ nodes | to_json }}),
// which reads every register. The template grammar has only literal keys
// and indexes, so a path of two or more parts always names its register.
func TemplateReads(tmpl render.Template) (names []string, all bool) {
	seen := map[string]bool{}
	for _, expr := range tmpl.Expressions() {
		if root := expr.Path[0]; root != TemplateRootNodes && root != TemplateRootResult {
			continue
		}
		if len(expr.Path) < 2 {
			all = true
			continue
		}
		seen[expr.Path[1]] = true
	}
	return sortedKeys(seen), all
}

// TaskReads returns every register task reads before or while it runs: the
// ones its condition reads (ConditionReads), the ones its parameters'
// templates read (TemplateReads, compiled through eng), the register its
// secret_mask marks, which it reads to find the values to mask, and what its
// authored rollback: steps read, since a block's rollback_on_failure runs
// them inside the forward run, each prepared as a task is
// (rollback_on_failure.go's runUndoStep).
func TaskReads(eng render.Engine, task *Task) (Reads, error) {
	seen := map[string]bool{}
	names, all, err := ConditionReads(task.Conditional)
	if err != nil {
		return Reads{}, err
	}
	for _, name := range names {
		seen[name] = true
	}
	for _, source := range TemplateStrings(task.Params) {
		tmpl, err := eng.Compile(source)
		if err != nil {
			return Reads{}, fmt.Errorf("a parameter template does not compile: %w", err)
		}
		names, whole := TemplateReads(tmpl)
		all = all || whole
		for _, name := range names {
			seen[name] = true
		}
	}
	if task.SecretMask != nil && task.SecretMask.Register != "" {
		seen[task.SecretMask.Register] = true
	}
	for i := range task.Rollback {
		step, err := TaskReads(eng, &task.Rollback[i])
		if err != nil {
			return Reads{}, fmt.Errorf("rollback step %d: %w", i+1, err)
		}
		all = all || step.All
		for _, name := range step.Names {
			seen[name] = true
		}
	}
	return Reads{Names: sortedKeys(seen), All: all}, nil
}
