// Package engine: when an optional field or index in a condition
// (result.?name, vars[?"name"]) is written for the name's absence, and when
// it is a plain read that fails without the name.
package engine

import (
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
)

// optionalSafeCalls are the member functions that consume an optional
// without failing on an empty one.
var optionalSafeCalls = map[string]bool{"hasValue": true, "orValue": true, "or": true}

// SafeOptionalReads returns the ids of every optional field or index in root
// (the _?._ and _[?_] calls the parser writes for x.?name and x[?"name"])
// whose optional is consumed safely, so the read is written for the name's
// absence: its chain of optional steps (x.?a.?b) ends in hasValue(),
// orValue(...) or or(...), is the optional argument of or(...), or is what
// optMap or optFlatMap binds (the macro's comprehension, which tests
// hasValue() before it takes the value). Every other optional read is a plain
// read: x.?name.value() fails on an absent name as x.name does.
func SafeOptionalReads(root celast.Expr) map[int64]bool {
	parents := map[int64]celast.Expr{}
	var walk func(e celast.Expr)
	walk = func(e celast.Expr) {
		for _, child := range exprChildren(e) {
			parents[child.ID()] = e
			walk(child)
		}
	}
	walk(root)
	safe := map[int64]bool{}
	celast.PreOrderVisit(root, celast.NewExprVisitor(func(e celast.Expr) {
		if !isOptionalStep(e) {
			return
		}
		// Climb the chain of optional steps this one starts (x.?a.?b) to its
		// last step; what consumes that one decides.
		last := e
		for p, ok := parents[last.ID()]; ok && isOptionalStep(p) && p.AsCall().Args()[0].ID() == last.ID(); p, ok = parents[last.ID()] {
			last = p
		}
		if consumesSafely(parents[last.ID()], last) {
			safe[e.ID()] = true
		}
	}))
	return safe
}

// isOptionalStep reports whether e is an optional field or index.
func isOptionalStep(e celast.Expr) bool {
	if e == nil || e.Kind() != celast.CallKind {
		return false
	}
	name := e.AsCall().FunctionName()
	return (name == operators.OptSelect || name == operators.OptIndex) && len(e.AsCall().Args()) == 2
}

// consumesSafely reports whether parent uses the optional child without
// failing on an empty one.
func consumesSafely(parent, child celast.Expr) bool {
	if parent == nil {
		return false
	}
	switch parent.Kind() {
	case celast.CallKind:
		call := parent.AsCall()
		// x.hasValue(), x.orValue(d), x.or(y): the optional is the target.
		if call.IsMemberFunction() && call.Target().ID() == child.ID() {
			return optionalSafeCalls[call.FunctionName()]
		}
		// y.or(x): the optional is the alternative.
		return call.IsMemberFunction() && call.FunctionName() == "or"
	case celast.ComprehensionKind:
		// optMap and optFlatMap bind a target that is not an identifier as a
		// comprehension's accumulator, then test hasValue() on it.
		return parent.AsComprehension().AccuInit().ID() == child.ID()
	}
	return false
}

// exprChildren returns e's direct subexpressions.
func exprChildren(e celast.Expr) []celast.Expr {
	switch e.Kind() {
	case celast.CallKind:
		call := e.AsCall()
		var out []celast.Expr
		if call.IsMemberFunction() {
			out = append(out, call.Target())
		}
		return append(out, call.Args()...)
	case celast.SelectKind:
		return []celast.Expr{e.AsSelect().Operand()}
	case celast.ListKind:
		return e.AsList().Elements()
	case celast.MapKind:
		var out []celast.Expr
		for _, entry := range e.AsMap().Entries() {
			m := entry.AsMapEntry()
			out = append(out, m.Key(), m.Value())
		}
		return out
	case celast.StructKind:
		var out []celast.Expr
		for _, f := range e.AsStruct().Fields() {
			out = append(out, f.AsStructField().Value())
		}
		return out
	case celast.ComprehensionKind:
		c := e.AsComprehension()
		return []celast.Expr{c.IterRange(), c.AccuInit(), c.LoopCondition(), c.LoopStep(), c.Result()}
	}
	return nil
}
