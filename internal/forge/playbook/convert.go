// Package playbook: converting one task, with one loop item bound, into
// native tasks.
package playbook

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// convertOnce converts spec with bindings (a loop item, or nil) through
// e, into one native task per call the entry chooses. suffix tells loop
// copies apart in their names.
func (t *translator) convertOnce(e *Entry, spec leafSpec, args []argIn, bindings map[string]any, suffix string, ctx taskCtx) []*outTask {
	name := spec.name
	if suffix != "" {
		name = fmt.Sprintf("%s (%s)", name, suffix)
	}
	cites := slices.Clone(spec.cites)
	whens, blockedBy, extra := t.whens(ctx.whens, spec.whens, bindings, spec.name)
	cites = append(cites, extra...)
	if blockedBy != "" {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, blockedBy)...)}
	}
	p := t.apply(e, args, bindings, spec.at)
	for _, n := range p.notes {
		cites = append(cites, t.raise(n.code, n.at, spec.name, n.why))
	}
	if p.blocked != nil {
		id := t.raise(p.blocked.code, p.blocked.at, spec.name, p.blocked.why)
		out := t.placeholder(name, spec.module, spec.at, ctx, append(cites, id)...)
		if p.computed {
			out.result.Class, out.result.ClassBasis = ClassComputed, "the state is chosen by a variable with no single value"
		}
		return []*outTask{out}
	}
	if spec.register != "" && len(p.calls) > 1 {
		id := t.raise("loop.register", spec.at, spec.name, "the task becomes several native tasks, and only one can hold the registered name")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, id)...)}
	}
	var out []*outTask
	for _, call := range p.calls {
		callName := name
		if call.label != "" {
			callName = fmt.Sprintf("%s (%s)", name, call.label)
		}
		if len(p.calls) > 1 && call.label == "" {
			callName = fmt.Sprintf("%s (%s)", name, call.call.FQCN)
		}
		if spec.checkMode {
			if ok, why := engine.Checkable(call.call.FQCN, call.params); !ok {
				id := t.raise("keyword.check_mode", spec.at, spec.name, why)
				out = append(out, t.placeholder(callName, spec.module, spec.at, ctx, append(cites, id)...))
				continue
			}
		}
		t.emitted++
		task := &outTask{
			name: callName, fqcn: call.call.FQCN, params: call.params, register: spec.register,
			when: whens, tags: spec.tags, checkMode: spec.checkMode, cites: cites, at: spec.at,
		}
		task.result = &TaskResult{
			At: spec.at, Name: callName, Module: spec.module, Runbook: ctx.runbook, FQCN: call.call.FQCN,
			ParamKeys: sortedKeys(call.params), Params: call.params,
			Class: call.call.Class, ClassBasis: call.call.Basis, Outcome: outcomeOf(t, cites), Findings: cites,
		}
		out = append(out, task)
	}
	if spec.register != "" && len(out) == 1 && out[0].result.Outcome != OutcomeBlocked {
		t.registers[spec.register] = produced{fqcn: out[0].fqcn, returns: e.Returns}
	}
	return out
}

// whens translates a task's conditions, after the ones its blocks pushed
// down, into CEL. It returns the conditions, the finding that blocks the
// task when one cannot be translated, and findings to cite.
func (t *translator) whens(inherited []string, own []*yaml.Node, bindings map[string]any, task string) ([]string, string, []string) {
	out := slices.Clone(inherited)
	var cites []string
	for _, w := range own {
		w = deref(w)
		at := nodePos(w, t.file)
		if w == nil || w.Kind != yaml.ScalarNode {
			return nil, t.raise("when.unsupported", at, task, "a condition that is not text"), cites
		}
		cel, readsRegister, cerr := t.condition(w.Value, bindings, at)
		if cerr != nil {
			return nil, t.raise(cerr.code, at, task, "when: "+cerr.why), cites
		}
		if readsRegister {
			cites = append(cites, t.raise("when.register", at, task, "the condition reads a registered result"))
		}
		if cel != "" {
			out = append(out, cel)
		}
	}
	return out, "", cites
}

// condition translates one Jinja condition into CEL: "" when it is always
// true, "false" when it is always false.
func (t *translator) condition(src string, bindings map[string]any, at Position) (string, bool, *celError) {
	e, err := parseWhen(src)
	if err != nil {
		return "", false, unsupported("%v", err)
	}
	c := &celCtx{r: t.res, bindings: bindings, use: at, registers: t.registers, perDevice: map[string]bool{}}
	term, cerr := c.toCEL(e)
	if cerr != nil {
		return "", false, cerr
	}
	if term.constant {
		if term.value.(bool) {
			return "", false, nil
		}
		return "false", false, nil
	}
	// The whole condition is quantified once, so it holds exactly when it
	// holds on every device: all(d, not x) rather than not all(d, x).
	switch len(c.perDevice) {
	case 0:
		return term.cel, c.readsRegister, nil
	case 1:
		for register := range c.perDevice {
			return wrapAll(register, term.cel), true, nil
		}
	}
	return "", false, unsupported("a condition reading more than one registered result, whose devices may differ")
}

// outcomeOf is a converted task's outcome: review when any finding it
// cites asks a person to read something, converted otherwise.
func outcomeOf(t *translator, cites []string) Outcome {
	for _, id := range cites {
		if f := t.finding(id); f != nil && f.Outcome == OutcomeReview {
			return OutcomeReview
		}
	}
	return OutcomeConverted
}
