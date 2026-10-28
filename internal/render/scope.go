// Package render: the static analysis of a template's scopes, so every
// read through a loop variable or a set name is reported as a read of what
// it was bound from.
package render

import (
	"slices"
	"strconv"
	"strings"
)

// Static scope analysis: which paths a template reads and how each read's
// value is used, computed from the node tree without rendering it.
//
// A name a statement binds is not a root a caller supplied, so a read
// through one is reported as a read of what the name was bound from: in
// {% for h in vars.hosts %}{{ h }}{% endfor %}, {{ h }} reads vars.hosts.
// That keeps every caller's root rules whole (a parameter may read only
// vars, nodes, result; a project path only vars) without each caller
// knowing statements exist, and it makes Names report the caller's names
// and never a loop variable. Where a name may or may not be bound (a set
// inside an if), both readings are reported, the safe direction.
//
// A binding carries only the reads whose value can become the bound value:
// {% set on = vars.env == 'prod' %} binds a boolean, so a later {{ on }}
// reads nothing, while vars.env is reported, as a condition, at the set.

// maxBindingOrigins bounds how many different values one name may be bound
// from. Each read of the name is reported once per value, so without a
// bound a template of many ifs setting one name and many reads of it
// reports their product. Past the bound the values collapse to their roots
// (bounded), so only a name bound from many different roots is refused.
const maxBindingOrigins = 16

// origin is one value a bound name may hold: the path it was read from,
// and whether that read was guarded (default, or a defined test).
type origin struct {
	path     []string
	optional bool
}

// key identifies an origin for deduplication.
func (o origin) key() string {
	return strconv.FormatBool(o.optional) + "\x00" + strings.Join(o.path, "\x00")
}

// binding is what analysis knows of one name in one scope.
type binding struct {
	// origins are the values the name may hold. A loop's own loop variable
	// has none: its counters read nothing the caller supplied.
	origins []origin

	// partial reports that the name is bound on some paths through the
	// template only, so a read may also reach the scope outside.
	partial bool
}

// analyzer walks a node tree with a stack of scopes mirroring the ones a
// render builds, plus one overlay per if branch being walked, which is
// where a set inside that branch lands until the branches are merged.
type analyzer struct {
	scopes []map[string]*binding
	out    []ExpressionInfo

	// guards are the paths a defined test the evaluation passed to get here
	// proves exist: an if's condition over its branch, an and's or an or's
	// earlier operands over its later ones (definedWhen). A read under one
	// is Guarded.
	guards []*exprNode
}

// analyze returns every read nodes makes, in source order, or an error
// when a name is bound from more roots than maxBindingOrigins.
func analyze(nodes []node) ([]ExpressionInfo, error) {
	a := &analyzer{scopes: []map[string]*binding{{}}}
	if err := a.walk(nodes); err != nil {
		return nil, err
	}
	return a.out, nil
}

// walk analyzes nodes in order.
func (a *analyzer) walk(nodes []node) error {
	for i := range nodes {
		n := &nodes[i]
		switch n.kind {
		case nodeOutput:
			a.report(n.expr, ExpressionOutput)

		case nodeSet:
			origins, err := bounded(n.name, a.report(n.expr, ExpressionAssignment))
			if err != nil {
				return err
			}
			a.scopes[len(a.scopes)-1][n.name] = &binding{origins: origins}

		case nodeIf:
			if err := a.walkIf(n.branches); err != nil {
				return err
			}

		case nodeFor:
			if err := a.walkFor(n.loop); err != nil {
				return err
			}
		}
	}
	return nil
}

// walkIf analyzes an if statement: each condition, then each branch in an
// overlay of its own, and merges what the branches set into the scope the
// if stands in, since an if opens no scope.
func (a *analyzer) walkIf(branches []branch) error {
	overlays := make([]map[string]*binding, 0, len(branches)+1)
	hasElse := false
	// A branch runs only when every condition before it was false, so what
	// those prove defined when false guards it, and its own condition, true.
	mark := len(a.guards)
	for _, br := range branches {
		var own []*exprNode
		if br.cond != nil {
			a.report(br.cond, ExpressionCondition)
			own = definedWhen(br.cond.body, true)
		} else {
			hasElse = true
		}
		overlay := map[string]*binding{}
		a.scopes = append(a.scopes, overlay)
		before := len(a.guards)
		a.guards = append(a.guards, own...)
		err := a.walk(br.body)
		a.guards = a.guards[:before]
		a.scopes = a.scopes[:len(a.scopes)-1]
		if err != nil {
			return err
		}
		overlays = append(overlays, overlay)
		if br.cond != nil {
			a.guards = append(a.guards, definedWhen(br.cond.body, false)...)
		}
	}
	a.guards = a.guards[:mark]
	if !hasElse {
		// No branch may run at all, which leaves every name as it was.
		overlays = append(overlays, map[string]*binding{})
	}

	parent := a.scopes[len(a.scopes)-1]
	names := map[string]bool{}
	for _, o := range overlays {
		for name := range o {
			names[name] = true
		}
	}
	for _, name := range sortedNames(names) {
		merged := &binding{}
		for _, o := range overlays {
			b, ok := o[name]
			if !ok {
				b, ok = parent[name]
			}
			if !ok {
				// Unbound on this path: a read reaches the scope outside.
				merged.partial = true
				continue
			}
			merged.partial = merged.partial || b.partial
			merged.origins = mergeOrigins(merged.origins, b.origins)
		}
		var err error
		if merged.origins, err = bounded(name, merged.origins); err != nil {
			return err
		}
		parent[name] = merged
	}
	return nil
}

// walkFor analyzes a for loop: its list in the enclosing scope, its body
// in a scope binding its names to the list and loop to nothing, and its
// else in a scope of its own.
func (a *analyzer) walkFor(loop *forLoop) error {
	origins, err := bounded(loop.targets[0], a.report(loop.iter, ExpressionIterable))
	if err != nil {
		return err
	}
	scope := map[string]*binding{loopName: {}}
	for _, t := range loop.targets {
		scope[t] = &binding{origins: origins}
	}
	a.scopes = append(a.scopes, scope)
	err = a.walk(loop.body)
	a.scopes = a.scopes[:len(a.scopes)-1]
	if err != nil {
		return err
	}
	a.scopes = append(a.scopes, map[string]*binding{})
	err = a.walk(loop.otherwise)
	a.scopes = a.scopes[:len(a.scopes)-1]
	return err
}

// resolve returns the values name may hold through bound names, and
// whether a read of it may also reach the caller's variable of that name.
func (a *analyzer) resolve(name string) (origins []origin, external bool) {
	for i := len(a.scopes) - 1; i >= 0; i-- {
		b, ok := a.scopes[i][name]
		if !ok {
			continue
		}
		origins = mergeOrigins(origins, b.origins)
		if !b.partial {
			return origins, false
		}
	}
	return origins, true
}

// mergeOrigins returns into with each of add it lacks appended.
func mergeOrigins(into, add []origin) []origin {
	for _, o := range add {
		k := o.key()
		if !slices.ContainsFunc(into, func(x origin) bool { return x.key() == k }) {
			into = append(into, o)
		}
	}
	return into
}

// sortedNames returns a set's names in sorted order, so analysis is
// deterministic.
func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}
