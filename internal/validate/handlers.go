// Package validate: HandlersRule, which checks what a block's rescue: makes
// possible: a failure only a rescue may read, and a register a rescued
// block may not have written.
package validate

import (
	"fmt"
	"slices"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// HandlerRule is HandlersRule's RuleName.
const HandlerRule = "handlers"

// HandlersRule refuses two things a block's rescue: brings with it.
//
//   - error.* outside a rescue: task. The failure a rescue answers is bound
//     for its tasks alone (internal/engine's handlers.go), so a template or
//     a condition reading it anywhere else would fail when the task runs.
//   - A register written only by a block's own tasks, read by a task after a
//     block that has a rescue:. When the rescue answers a failure the run
//     goes on, and the task that failed, or any after it in the block, never
//     wrote what it registers; the read would fail when it renders. It is
//     allowed when the rescue registers the same name too, when another task
//     outside the block does, or when the read is written for absence: the
//     expression ends with | default(...), or the condition tests the
//     register with has(...).
func HandlersRule(world WorldView) []Finding {
	dag := world.DAG
	eng := render.New()
	ids := make([]string, 0, len(dag.Nodes))
	for id := range dag.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var findings []Finding
	add := func(id, message string) {
		findings = append(findings, Finding{RuleName: HandlerRule, Node: id, Message: message})
	}

	// error outside a rescue.
	for _, id := range ids {
		task := dag.Nodes[id]
		if engine.InPart(dag.Scopes[id], engine.PartRescue) {
			continue
		}
		for _, source := range engine.TemplateStrings(task.Params) {
			tmpl, err := eng.Compile(source)
			if err != nil {
				continue // TemplateRule's finding
			}
			for _, expr := range tmpl.Expressions() {
				if expr.Path[0] == engine.TemplateRootError {
					add(id, fmt.Sprintf("task %s reads error, which is the failure a rescue: answers and is there only for a rescue: task", taskName(id, task)))
				}
			}
		}
		if uses, err := engine.ConditionUsesRoot(task.Conditional, engine.TemplateRootError); err == nil && uses {
			add(id, fmt.Sprintf("task %s's condition reads error, which is the failure a rescue: answers and is there only for a rescue: task", taskName(id, task)))
		}
	}

	// Registers a rescued block may not have written. The build refuses a
	// cycle, the one thing TopologicalOrder errors on; with no order there is
	// nothing after a block to check.
	order, _ := engine.TopologicalOrder(dag)
	position := make(map[string]int, len(order))
	for i, id := range order {
		position[id] = i
	}
	blocks := make([]string, 0, len(dag.Guards))
	for block, guard := range dag.Guards {
		if guard.HasRescue() {
			blocks = append(blocks, block)
		}
	}
	sort.Strings(blocks)
	for _, block := range blocks {
		guard := dag.Guards[block]
		maybe := mayNotRegister(dag, guard)
		if len(maybe) == 0 {
			continue
		}
		// The block ends with its always: when it has one, else its rescue.
		end := guard.Rescue
		if guard.HasAlways() {
			end = guard.Always
		}
		last := position[end[len(end)-1]]
		for _, id := range order[last+1:] {
			task := dag.Nodes[id]
			for _, name := range unguardedReads(eng, task, maybe) {
				add(id, fmt.Sprintf("task %s reads the register %q, which only %s's own tasks write, so after its rescue: answers a failure it does not exist: "+
					"register it in the rescue: too, or read it with | default(...) or test it with has(...)", taskName(id, task), name, block))
			}
		}
	}
	return findings
}

// mayNotRegister returns the registers only a block's own tasks (guard.Tasks)
// write: not its rescue, and no task outside the block.
func mayNotRegister(dag *engine.DAG, guard engine.BlockGuard) map[string]bool {
	inBlock := map[string]bool{}
	for _, id := range guard.Tasks {
		if r := dag.Nodes[id].Register; r != "" {
			inBlock[r] = true
		}
	}
	for id, task := range dag.Nodes {
		if task.Register == "" || !inBlock[task.Register] || slices.Contains(guard.Tasks, id) {
			continue
		}
		// Written by the rescue, or anywhere outside the block's own tasks.
		delete(inBlock, task.Register)
	}
	return inBlock
}

// unguardedReads returns the names in maybe that task reads without
// handling their absence, sorted.
func unguardedReads(eng render.Engine, task *engine.Task, maybe map[string]bool) []string {
	reads := map[string]bool{}
	for _, source := range engine.TemplateStrings(task.Params) {
		tmpl, err := eng.Compile(source)
		if err != nil {
			continue
		}
		for _, expr := range tmpl.Expressions() {
			root := expr.Path[0]
			if (root != engine.TemplateRootNodes && root != engine.TemplateRootResult) || len(expr.Path) < 2 || !maybe[expr.Path[1]] {
				continue
			}
			// A read the renderer marks Guarded (under default on the path
			// itself, tested with is defined, or reached only past such a
			// test) handles the register's absence itself.
			if !expr.Guarded {
				reads[expr.Path[1]] = true
			}
		}
	}
	if names, _, err := engine.ConditionReads(task.Conditional); err == nil {
		tested, _ := engine.ConditionTestsPresence(task.Conditional)
		for _, name := range names {
			if maybe[name] && !tested[name] {
				reads[name] = true
			}
		}
	}
	out := make([]string, 0, len(reads))
	for name := range reads {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func init() {
	Register(HandlersRule)
}
