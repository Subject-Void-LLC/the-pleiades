// Package validate: VarsRule, which finds a task reading a run variable that
// is defined nowhere it can see, before anything runs.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// defaultFilter is the render filter that makes a missing value optional
// out loud ({{ vars.region | default('us-east-1') }}).
const defaultFilter = "default"

// VarsRule refuses a task that reads vars.<name> when no scope it can see
// defines name, the failure the renderer would otherwise report only when the
// task dispatches, after the tasks before it have already run. A name is
// defined for a task by any of: the runbook's vars:, the task's own vars: or
// an enclosing block's, a global set_fact that runs before it, a set_fact that
// runs before it, the vars property of a device it targets (the static
// inventory's group_vars and host_vars), or a variable the run is given
// (WorldView.Variables). A name that is set only by a set_fact further on in
// the runbook, or only in another block's or task's vars:, is refused with
// that said, since it is the likelier mistake than a name typed wrong.
//
// It reads a template's expressions (render.Template.Expressions) and a
// condition's syntax tree, never the text around them. A read guarded by
// | default(...) is optional, and so is a name the task's condition tests with
// has(vars.<name>) or "<name>" in vars.
//
// It applies only when the caller knows every variable the run will have
// (WorldView.VariablesKnown); otherwise it finds nothing. A task with a
// when: of its own is left to the engine for its parameters, as TransportRule
// leaves it: its condition may skip it, and a skipped task reads no
// parameter; the condition itself is always read. Each task's rollback: steps
// are read too (named <node>.rollback[<i>]), since they render with the same
// variables when rollback_on_failure undoes the task, and with none at all
// when `pleiades rollback` replays them; a step of a conditional task is left
// to the engine with its task.
//
// The advice names how to supply a value only where the caller takes one
// (WorldView.VariablesFlag); a command that takes none is told so.
func VarsRule(world WorldView) []Finding {
	if !world.VariablesKnown {
		return nil
	}
	eng := render.New()
	scopes := newVarScopes(world)
	var findings []Finding
	for _, pt := range paramTasks(world.DAG) {
		reads := readsOf(eng, pt)
		if len(reads) == 0 {
			continue
		}
		// A name the task's condition tests for is written for its absence.
		guarded := map[string]bool{}
		for _, r := range reads {
			if r.optional {
				guarded[r.name] = true
			}
		}
		conditional := world.DAG.Conditional(pt.node)
		devices := scopes.deviceVarNames(world.DAG.Nodes[pt.node])
		seen := map[string]bool{}
		for _, r := range reads {
			if r.optional || guarded[r.name] || (conditional && r.where != "condition") {
				continue
			}
			if _, supplied := world.Variables[r.name]; supplied {
				continue
			}
			defs, late := scopes.definitions(pt.node, r.name, devices)
			if len(defs) > 0 || seen[r.where+"\x00"+r.name] {
				continue
			}
			seen[r.where+"\x00"+r.name] = true
			findings = append(findings, Finding{
				RuleName: "vars",
				Node:     pt.id,
				Message: fmt.Sprintf("task %s, %s: no value is supplied for vars.%s; %s",
					taskName(pt.id, pt.task), r.where, r.name, varsAdvice(world, pt.node, r.name, late)),
			})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Node < findings[j].Node })
	return findings
}

// varsAdvice is how to fix a missing vars.<name>: say where it is set but out
// of reach, if it is, then give it with flag, where the command takes one, or
// otherwise write the value or a default, since nothing this command does can
// supply it.
func varsAdvice(world WorldView, node, name string, late []factSite) string {
	var b strings.Builder
	if len(late) > 0 {
		fmt.Fprintf(&b, "it is set by the set_fact at %s, which does not run before this task; ", late[0].node)
	} else if other := otherDefiner(world, node, name); other != "" {
		fmt.Fprintf(&b, "it is defined only in the vars: of %s, which this task is not inside; ", other)
	}
	if world.VariablesFlag == "" {
		b.WriteString("this command takes no variables, so write the value itself, or make it optional with | default(...)")
	} else {
		fmt.Fprintf(&b, "give it with %s %s=<value>, or make it optional with | default(...)", world.VariablesFlag, name)
	}
	b.WriteString(", or define it in vars:, in a set_fact before this task, or in the device's group_vars or host_vars")
	return b.String()
}

// otherDefiner returns the first node, by id, other than node whose own vars:
// define name, or "".
func otherDefiner(world WorldView, node, name string) string {
	ids := make([]string, 0)
	for id, task := range world.DAG.Nodes {
		if _, ok := task.Vars[name]; ok && id != node {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return ids[0]
}

// init registers VarsRule with the rules Validate runs.
func init() {
	Register(VarsRule)
}
