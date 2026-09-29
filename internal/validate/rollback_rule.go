// Package validate: the two rules for a runbook's rollback keys (Phase 40).
// RollbackRule holds a task's rollback: steps to what any task is held to;
// ReversibleRule holds a runbook marked reversible: true to its word.
package validate

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// RollbackRule holds every task's rollback: steps to exactly what the
// runbook's own tasks are held to: a method that is registered and
// implemented (CollectionRule), and only parameters it declares
// (ParamsRule). It runs those two rules over a view whose tasks are the
// steps, rather than restating either, so a step can never pass something
// a task would not. A step is found here, at validation, rather than at
// the rollback that needs it, which may be long after the run and the one
// moment a typo in an undo costs the most.
func RollbackRule(world WorldView) []Finding {
	steps := &engine.DAG{Nodes: map[string]*engine.Task{}}
	for id, task := range world.DAG.Nodes {
		for i := range task.Rollback {
			steps.Nodes[fmt.Sprintf("%s.rollback[%d]", id, i)] = &task.Rollback[i]
		}
	}
	if len(steps.Nodes) == 0 {
		return nil
	}
	view := WorldView{Items: world.Items, DAG: steps, Mode: world.Mode, Resolver: world.Resolver}
	var findings []Finding
	for _, rule := range []Rule{CollectionRule, ParamsRule} {
		for _, f := range rule(view) {
			f.RuleName = "rollback"
			findings = append(findings, f)
		}
	}
	return findings
}

// deviceUnchangingActions are the engine's own actions that change no
// device: noop echoes its params, set_metadata writes the run's metadata,
// and ios_backup reads a configuration. Every other engine action runs an
// arbitrary command on a device (ssh_exec and its siblings).
var deviceUnchangingActions = map[string]bool{
	"noop":                          true,
	"set_metadata":                  true,
	"pleiades.builtin.set_metadata": true,
	"ios_backup":                    true,
}

// ReversibleRule refuses a runbook marked reversible: true that holds a
// task a rollback could not undo: one calling a method whose recorded undo
// would not replay whole, with no rollback: list of its own. A read-only
// task passes, and so does any task with an authored undo, whatever its
// method. The point is to learn this before the run, from the runbook,
// rather than from a rollback that refuses after the change is made.
func ReversibleRule(world WorldView) []Finding {
	if !world.DAG.Reversible {
		return nil
	}
	var findings []Finding
	for id, task := range world.DAG.Nodes {
		if task.Kind() != engine.TaskKindLeaf || len(task.Rollback) > 0 {
			continue
		}
		why := whyNotUndoable(task.FQCN)
		if why == "" {
			continue
		}
		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}
		findings = append(findings, Finding{
			RuleName: "reversible",
			Node:     id,
			Message: fmt.Sprintf("the runbook is marked reversible, and task %s %s; give the task a rollback: list saying how to undo it, or remove reversible: true",
				label, why),
		})
	}
	return findings
}

// whyNotUndoable says why a task calling fqcn could not be undone from its
// journal, or "" when it could (or changes no device). A name nothing
// registers is CollectionRule's finding, not this rule's.
func whyNotUndoable(fqcn string) string {
	if !isCollectionName(fqcn) || dottedBuiltinExemptions[fqcn] {
		if deviceUnchangingActions[fqcn] {
			return ""
		}
		return fmt.Sprintf("runs %s, an arbitrary command whose effect no recorded undo can reverse", fqcn)
	}
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		return ""
	}
	r := desc.Manifest.Reversibility
	switch {
	case desc.Provider != nil:
		// Neither ReadOnly nor Inverses is honored for a third party's
		// method, so its undo is never replayed.
		return fmt.Sprintf("calls %s, an external program's method, whose undo a rollback does not replay", fqcn)
	case r.ReadOnly:
		return ""
	case !r.Reversible:
		return fmt.Sprintf("calls %s, which cannot be undone (%s)", fqcn, strings.TrimSuffix(r.Notes, "."))
	case len(r.Inverses) == 0:
		return fmt.Sprintf("calls %s, whose undo is recorded by name only", fqcn)
	}
	for _, spec := range r.Inverses {
		if len(spec.Withhold) > 0 {
			return fmt.Sprintf("calls %s, whose undo through %s keeps %s out of the journal, so no rollback can replay it",
				fqcn, spec.FQCN, strings.Join(spec.Withhold, " and "))
		}
		if spec.MayBePartial {
			return fmt.Sprintf("calls %s, whose undo through %s can leave part of what it changed in place", fqcn, spec.FQCN)
		}
	}
	return ""
}

func init() {
	Register(RollbackRule)
	Register(ReversibleRule)
}
