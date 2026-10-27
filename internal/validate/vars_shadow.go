// Package validate: VarShadowWarnings, which says when a variable a task reads
// is defined in more than one of its scopes.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// VarShadowWarnings returns one warning for each variable a task reads that
// is defined in two or more scopes of its chain (the runbook, a global
// set_fact, the device, a set_fact, a block, the task), naming the scopes and
// the one that wins, which is the narrowest. Shadowing is legal, so these are
// advice and never fail validation. The run's extra variables always win and
// are not a scope here.
//
// Each distinct case is said once for the runbook, at the first task that
// meets it, so a runbook variable a device overrides is not repeated for every
// task that reads it. Only a variable the task actually reads is considered.
// A block's vars: and a task's own are told apart only when the task's value
// differs, since the builder copies a block's down onto its tasks.
func VarShadowWarnings(world WorldView) []Finding {
	eng := render.New()
	scopes := newVarScopes(world)
	said := map[string]bool{}
	var out []Finding
	for _, pt := range paramTasks(world.DAG) {
		if pt.id != pt.node {
			continue
		}
		reads := readsOf(eng, pt)
		if len(reads) == 0 {
			continue
		}
		devices := scopes.deviceVarNames(pt.task)
		names := map[string]bool{}
		for _, r := range reads {
			names[r.name] = true
		}
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, name := range sorted {
			defs, _ := scopes.definitions(pt.node, name, devices)
			if len(defs) < 2 {
				continue
			}
			parts := make([]string, len(defs))
			for i, d := range defs {
				parts[i] = d.what
			}
			key := name + "\x00" + strings.Join(parts, "\x00")
			if said[key] {
				continue
			}
			said[key] = true
			out = append(out, Finding{
				RuleName: "vars",
				Node:     pt.id,
				Message: fmt.Sprintf("task %s: vars.%s is defined in %s; the narrowest, %s, is the one read",
					taskName(pt.id, pt.task), name, strings.Join(parts, " and in "), parts[len(parts)-1]),
			})
		}
	}
	return out
}
