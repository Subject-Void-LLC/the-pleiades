// Package validate: RequiredParamsRule, which refuses a task that leaves
// out a parameter its method marks required.
package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// RequiredParamsRule flags a task that does not pass a parameter its
// Collection method marks Required, in its own Doc.Params or through a
// named fragment. The method refuses such a call itself, but only when the
// run reaches it, after the tasks before it have changed things; this
// refuses it before the run starts. A parameter given by an alias
// (collection.Param.AliasOf) is given; one given as null is not; and a
// value holding a template is, since it renders to something the engine
// checks when the task runs.
//
// Like ParamsRule, it checks only a method that is registered and
// implemented.
func RequiredParamsRule(world WorldView) []Finding {
	var findings []Finding
	for id, task := range world.DAG.Nodes {
		if !isCollectionName(task.FQCN) || dottedBuiltin(task.FQCN) {
			continue
		}
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || desc.Manifest.Status != collection.StatusImplemented {
			continue
		}
		var missing []string
		for _, req := range requiredParams(desc) {
			if !givenAny(task.Params, req.names) {
				missing = append(missing, req.name)
			}
		}
		if len(missing) == 0 {
			continue
		}
		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}
		findings = append(findings, Finding{
			RuleName: "param-required",
			Node:     id,
			Message: fmt.Sprintf("task %s calls %s without %s, which it requires, so the run would fail there after the tasks before it had run; pleiades doc %s describes %s",
				label, task.FQCN, quoteAll(missing), task.FQCN, plural(len(missing), "it", "them")),
		})
	}
	return findings
}

// required is one parameter a method requires, and every name a task may
// give it by: its own, then its aliases'.
type required struct {
	name  string
	names []string
}

// requiredParams returns, sorted by name, every parameter d's
// documentation marks required, its own Doc.Params' and its named
// fragments', each with the aliases that stand for it.
func requiredParams(d collection.Descriptor) []required {
	shared, _ := fragment.Params(d.Manifest.Doc.Fragments)
	params := append(shared, d.Manifest.Doc.Params...)
	var out []required
	for _, p := range params {
		if !p.Required || slices.ContainsFunc(out, func(r required) bool { return r.name == p.Name }) {
			continue
		}
		r := required{name: p.Name, names: []string{p.Name}}
		for _, alias := range params {
			if alias.AliasOf == p.Name {
				r.names = append(r.names, alias.Name)
			}
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b required) int { return strings.Compare(a.name, b.name) })
	return out
}

// givenAny reports whether params gives any of names a value; null is no
// value.
func givenAny(params map[string]any, names []string) bool {
	for _, name := range names {
		if v, given := params[name]; given && v != nil {
			return true
		}
	}
	return false
}

func init() {
	Register(RequiredParamsRule)
}
