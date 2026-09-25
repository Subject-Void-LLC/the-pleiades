// Package validate: ParamsRule, which refuses a task parameter the method
// it calls does not declare.
package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// engineParamKeys are the params the engine itself reads from every task,
// whatever method it calls: target, the device selector TaskTarget
// resolves (internal/engine/action.go). No method declares it, and it is
// always allowed.
var engineParamKeys = map[string]bool{"target": true}

// ParamsRule flags a task that passes its Collection method a parameter
// the method does not declare, in its own Doc.Params or through a shared
// fragment (internal/catalog/fragment). A method reads only the keys it
// knows, so an undeclared one was silently dropped: a misspelled key
// (commnd for command) or a key copied from a different method ran as
// though it had never been written. A misspelled optional parameter is
// the dangerous case, since the method then applies its default.
//
// It checks only a method that is registered and implemented. An unknown
// or declared-only method is CollectionRule's finding already, and an
// engine action (noop, ssh_exec, import_tasks and the rest) has no
// documented parameters to check against. A method whose documentation
// is incomplete (an external Collection that ships no Doc.Params, say) is
// held to what it declares: an undocumented parameter is refused rather
// than guessed at, and the fix is to document it.
func ParamsRule(world WorldView) []Finding {
	var findings []Finding
	for id, task := range world.DAG.Nodes {
		if len(task.Params) == 0 || !isCollectionName(task.FQCN) || dottedBuiltinExemptions[task.FQCN] {
			continue
		}
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || desc.Manifest.Status != collection.StatusImplemented {
			continue
		}
		declared := fragment.Declared(desc)
		var undeclared []string
		for key := range task.Params {
			if !declared[key] && !engineParamKeys[key] {
				undeclared = append(undeclared, key)
			}
		}
		if len(undeclared) == 0 {
			continue
		}
		slices.Sort(undeclared)
		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}
		accepted := make([]string, 0, len(declared))
		for k := range declared {
			accepted = append(accepted, k)
		}
		slices.Sort(accepted)
		findings = append(findings, Finding{
			RuleName: "params",
			Node:     id,
			Message: fmt.Sprintf("task %s passes %s %s, which %s does not declare, so the method would ignore %s; %s accepts %s",
				label, plural(len(undeclared), "parameter", "parameters"), quoteAll(undeclared), task.FQCN,
				plural(len(undeclared), "it", "them"), task.FQCN, acceptedList(accepted)),
		})
	}
	return findings
}

// plural returns one when n is 1 and many otherwise.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// quoteAll renders names as a comma-separated list of quoted strings.
func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}

// acceptedList renders a method's declared parameters, or says it
// declares none.
func acceptedList(names []string) string {
	if len(names) == 0 {
		return "no parameters"
	}
	return strings.Join(names, ", ")
}

func init() {
	Register(ParamsRule)
}
