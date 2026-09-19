// Package engine: a condition list evaluated with some registered results
// unknown.
package engine

import "fmt"

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
	sawUnknown := false
	falseExprs := make([]string, 0, len(cp.items))
	for i, item := range cp.items {
		value, known, err := item.prg.EvalPartial(vars, unknown)
		switch {
		case err != nil:
			if sawUnknown {
				return ConditionResult{}, false, nil
			}
			return ConditionResult{}, true, fmt.Errorf("failed to evaluate %s expression %d (`%s`): %w", cp.keyword, i+1, item.expr, err)
		case !known:
			sawUnknown = true
		case cp.or && value:
			return ConditionResult{OK: true}, true, nil
		case !cp.or && !value:
			return cp.andSkip(i), true, nil
		case cp.or:
			falseExprs = append(falseExprs, fmt.Sprintf("`%s`", item.expr))
		}
	}
	if sawUnknown {
		return ConditionResult{}, false, nil
	}
	if !cp.or {
		return ConditionResult{OK: true}, true, nil
	}
	return cp.orSkip(falseExprs), true, nil
}
