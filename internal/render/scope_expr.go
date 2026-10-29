// Package render: what each expression reads, its Kind, and whether an
// is defined test guards it, for the analysis in scope.go.
package render

import (
	"fmt"
	"slices"
)

// The expression half of scope analysis: one ExpressionInfo per path an
// expression reads, with how its value is used.
//
// A read's value reaches the expression's own value through a filter, an
// and/or (which return an operand), a ~, a list or mapping literal, or a
// mapping method; it does not through a comparison, an in, a not or a
// test, each of which gives only true or false. A read of the second kind
// is reported as ExpressionCondition wherever it stands, which is what
// lets a caller's command-text rule ask | quote only of what can reach the
// text: "true" and "false" carry no device syntax.

// report records every read e makes, and returns the origins e's value may
// carry, which a statement binds. kind is where e stands.
func (a *analyzer) report(e *expression, kind ExpressionKind) []origin {
	return a.walkExpr(e.body, flow{kind: kind})
}

// flow is how a node's value is used: where it ends up (kind), and the
// filters applied to it after this node, in order.
type flow struct {
	kind  ExpressionKind
	after []string
}

// tested is the flow of an operand whose value only decides a truth or a
// comparison: nothing of it reaches the enclosing value.
var tested = flow{kind: ExpressionCondition}

// walkExpr walks one node and returns the origins of its value.
func (a *analyzer) walkExpr(n *exprNode, f flow) []origin {
	switch n.kind {
	case exprRef:
		return a.reportRef(n, f, false)

	case exprAnd, exprOr:
		// Each operand's value can be the result. An operand is evaluated
		// only when the ones before it did not decide, so what they prove
		// defined guards it: "x is defined and x.ok" reads x.ok only when x
		// exists.
		mark := len(a.guards)
		var out []origin
		for _, arg := range n.args {
			out = mergeOrigins(out, a.walkExpr(arg, f))
			a.guards = append(a.guards, definedWhen(arg, n.kind == exprAnd)...)
		}
		a.guards = a.guards[:mark]
		return out

	case exprList, exprMap, exprConcat:
		// Each operand's value can be the result, or part of it.
		var out []origin
		for _, arg := range n.args {
			out = mergeOrigins(out, a.walkExpr(arg, f))
		}
		return out

	case exprMethod:
		return a.walkExpr(n.operand, f)

	case exprNot, exprCompare, exprTest:
		if n.operand != nil {
			if n.kind == exprTest && n.name == "defined" && n.operand.kind == exprRef {
				a.reportRef(n.operand, tested, true)
			} else {
				a.walkExpr(n.operand, tested)
			}
		}
		for _, arg := range n.args {
			a.walkExpr(arg, tested)
		}
		return nil

	case exprFilter:
		inner := flow{kind: f.kind, after: append([]string{n.name}, f.after...)}
		var out []origin
		if n.name == filterDefault && n.operand.kind == exprRef {
			out = a.reportRef(n.operand, inner, true)
		} else {
			out = a.walkExpr(n.operand, inner)
		}
		// default's fallback becomes the value in the operand's place, so
		// the filters after default apply to it, and default does not.
		for _, arg := range n.args {
			out = mergeOrigins(out, a.walkExpr(arg, f))
		}
		return out
	}
	return nil
}

// reportRef records a path's reads: the caller's variable, a bound name's
// origins, or both where the name may or may not be bound. guarded is a
// read that cannot fail on a missing name.
func (a *analyzer) reportRef(n *exprNode, f flow, guarded bool) []origin {
	guarded = guarded || a.guardedByDefined(n)
	var out []origin
	through, external := a.resolve(n.root)
	if external {
		path := n.path()
		a.out = append(a.out, ExpressionInfo{Path: path, Filters: nonEmpty(slices.Clone(f.after)), Kind: f.kind, Guarded: guarded})
		out = append(out, origin{path: path, optional: guarded})
	}
	for _, o := range through {
		var fs []string
		if o.optional {
			fs = append(fs, filterDefault)
		}
		fs = append(fs, f.after...)
		a.out = append(a.out, ExpressionInfo{Path: slices.Clone(o.path), Filters: nonEmpty(fs), Kind: f.kind, Guarded: guarded || o.optional})
		out = mergeOrigins(out, []origin{{path: o.path, optional: o.optional || guarded}})
	}
	return out
}

// definedWhen returns the paths n proves defined when its value's truth is
// truth: "x is defined" true, "x is not defined" false, an and of them true,
// an or of them false, and a not the other way round. Only a path tested
// directly counts, as only a path can be undefined.
func definedWhen(n *exprNode, truth bool) []*exprNode {
	switch n.kind {
	case exprTest:
		if n.name == "defined" && n.operand.kind == exprRef && truth != n.negate {
			return []*exprNode{n.operand}
		}
	case exprNot:
		return definedWhen(n.operand, !truth)
	case exprAnd, exprOr:
		// Every operand's truth is known only for the outcome that needs
		// all of them: an and that is true, an or that is false.
		if truth != (n.kind == exprAnd) {
			return nil
		}
		var out []*exprNode
		for _, arg := range n.args {
			out = append(out, definedWhen(arg, truth)...)
		}
		return out
	}
	return nil
}

// guardedByDefined reports whether a defined test the evaluation must have
// passed to reach this read covers its path: the same root, and steps the
// read's own begin with.
func (a *analyzer) guardedByDefined(n *exprNode) bool {
	for _, g := range a.guards {
		if g.root == n.root && len(g.steps) <= len(n.steps) && slices.Equal(g.steps, n.steps[:len(g.steps)]) {
			return true
		}
	}
	return false
}

// nonEmpty returns nil for an empty list, so an expression with no filters
// reports none rather than an empty list.
func nonEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// bounded holds a binding's origins to maxBindingOrigins. Past it, the
// origins collapse to their roots, a read of the whole root and the safe
// direction, and only a name bound from more roots than that is refused.
func bounded(name string, origins []origin) ([]origin, error) {
	if len(origins) <= maxBindingOrigins {
		return origins, nil
	}
	var roots []origin
	for _, o := range origins {
		roots = mergeOrigins(roots, []origin{{path: o.path[:1]}})
	}
	if len(roots) > maxBindingOrigins {
		return nil, fmt.Errorf("%w: %q is bound from more than %d different roots", ErrTooLarge, name, maxBindingOrigins)
	}
	return roots, nil
}
