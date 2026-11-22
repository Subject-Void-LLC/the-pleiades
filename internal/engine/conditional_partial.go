// Package engine: a condition list evaluated with some registered results
// unknown.
package engine

import (
	"fmt"
	"sort"
)

// EvalPartial is Eval for a check, with the registered results in unknown
// marked as not known (Program.EvalPartial). known is false when whether
// the task runs depends on one of them.
//
// Each list is walked in the order Eval walks it, and a real run's answer
// is reproduced wherever the unknowns cannot change it. For when (every
// item must hold): a false item skips the task whatever an earlier
// unknown item holds, since that item being false would have skipped it
// too; an error with no unknown before it is the real run's error; an
// error after an unknown is unknown, because the real run reaches it only
// if the unknown held. For when_or (any item holding runs it) the mirror
// image: a true item runs the task whatever an earlier unknown held, and
// an error after an unknown is unknown. A list that ends with an unknown
// still open is unknown.
func (cp *ConditionProgram) EvalPartial(vars map[string]interface{}, unknown []UnknownRegister) (ConditionResult, bool, error) {
	res, known, _, err := cp.evalPartialDeps(vars, unknown)
	return res, known, err
}

// evalPartialDeps is EvalPartial, saying what an unknown result depended on:
// what each item left unknown before the list was decided.
func (cp *ConditionProgram) evalPartialDeps(vars map[string]interface{}, unknown []UnknownRegister) (ConditionResult, bool, unknownDeps, error) {
	sawUnknown := false
	var deps unknownDeps
	falseExprs := make([]string, 0, len(cp.items))
	for i, item := range cp.items {
		value, known, itemDeps, err := evalItemPartial(item.prg, vars, unknown)
		switch {
		case err != nil:
			if sawUnknown {
				return ConditionResult{}, false, deps, nil
			}
			return ConditionResult{}, true, unknownDeps{}, &conditionItemError{keyword: cp.keyword, ordinal: i + 1, expr: item.expr, err: err}
		case !known:
			sawUnknown = true
			deps = mergeDeps(deps, itemDeps)
		case cp.or && value:
			return ConditionResult{OK: true}, true, unknownDeps{}, nil
		case !cp.or && !value:
			return cp.andSkip(i), true, unknownDeps{}, nil
		case cp.or:
			falseExprs = append(falseExprs, fmt.Sprintf("`%s`", item.expr))
		}
	}
	if sawUnknown {
		return ConditionResult{}, false, deps, nil
	}
	if !cp.or {
		return ConditionResult{OK: true}, true, unknownDeps{}, nil
	}
	return cp.orSkip(falseExprs), true, unknownDeps{}, nil
}

// evalItemPartial evaluates one item partially, with what an unknown answer
// depended on when the program can say (a celProgram), and as depending on
// something other than a fact when it cannot.
func evalItemPartial(prg Program, vars map[string]interface{}, unknown []UnknownRegister) (bool, bool, unknownDeps, error) {
	if p, ok := prg.(interface {
		evalPartialDeps(map[string]interface{}, []UnknownRegister) (bool, bool, unknownDeps, error)
	}); ok {
		return p.evalPartialDeps(vars, unknown)
	}
	value, known, err := prg.EvalPartial(vars, unknown)
	return value, known, unknownDeps{other: !known}, err
}

// mergeDeps is what two unknowns together depended on.
func mergeDeps(a, b unknownDeps) unknownDeps {
	out := unknownDeps{allFacts: a.allFacts || b.allFacts, other: a.other || b.other}
	seen := map[string]bool{}
	for _, k := range append(append([]string(nil), a.facts...), b.facts...) {
		if !seen[k] {
			seen[k] = true
			out.facts = append(out.facts, k)
		}
	}
	sort.Strings(out.facts)
	return out
}
