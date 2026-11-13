// Package rollback: an authored rollback: step's templated parameters, from
// the values the run rendered for them (engine.JournalEntry.AuthoredSteps).
//
// A step's text may read what only the run had: its variables, a register's
// result. A rollback is a new run with none of that, so a templated step is
// never rendered again here. Its values come from the journal, which recorded
// them on each device under the rules a recorded undo's are; a step the
// journal could not hold, or holds no copy of, is refused by name unless it
// reads only the device it runs on, which a rollback has as the run did.
package rollback

import (
	"fmt"
	"maps"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// byHand is what every refusal of a templated step tells its operator to do.
const byHand = "undo it by hand, or leave it in place; a block's rollback_on_failure undoes it inside the run, while those values exist"

// renderedParams returns the index-th step of e's rollback: list, task, as a
// rollback runs it: task's own params when none holds a template, or holds
// only templates reading the device; otherwise its literal params with each
// templated one replaced by the value the run journaled for it, and the keys
// so replaced. It refuses a templated step the journal cannot answer for.
func renderedParams(e engine.JournalEntry, index int, task engine.Task) (map[string]any, []string, error) {
	params := maps.Clone(task.Params)
	if !engine.HasTemplate(task.Params) {
		return params, nil, nil
	}
	var rec *engine.AuthoredStep
	for i := range e.AuthoredSteps {
		if e.AuthoredSteps[i].Index == index {
			rec = &e.AuthoredSteps[i]
		}
	}
	runOnly := readsRunOnly(task)
	switch {
	case rec != nil && rec.StepDigest != engine.AuthoredStepDigest(task):
		if !runOnly {
			// Edited since the run, and reading only the device: rendered by
			// the rollback run, as before the journal held steps.
			return params, nil, nil
		}
		return nil, nil, fmt.Errorf("step %d of its rollback: list was changed since the run and reads what only the run had (its variables or registered results), so the values the run rendered are for another step; %s", index+1, byHand)
	case rec == nil:
		if !runOnly {
			return params, nil, nil
		}
		return nil, nil, fmt.Errorf("step %d of its rollback: list reads what only the run had (its variables or registered results), and the journal holds no rendered copy of it (it was written before journals held one, or the step was added since the run); %s", index+1, byHand)
	case !rec.Complete:
		return nil, nil, fmt.Errorf("step %d of its rollback: list cannot be replayed from the journal: %s; %s", index+1, rec.Withheld.Describe(), byHand)
	}
	var rendered []string
	for _, p := range rec.Params {
		v, ok := p.Value()
		if !ok || !engine.HasTemplate(task.Params[p.Key]) {
			return nil, nil, fmt.Errorf("step %d of its rollback: list has a journaled value for %s that its runbook does not template; %s", index+1, Printable(p.Key), byHand)
		}
		params[p.Key] = v
		rendered = append(rendered, p.Key)
	}
	for key, v := range task.Params {
		if engine.HasTemplate(v) && !slices.Contains(rendered, key) {
			return nil, nil, fmt.Errorf("step %d of its rollback: list has no journaled value for its templated parameter %s; %s", index+1, Printable(key), byHand)
		}
	}
	slices.Sort(rendered)
	return params, rendered, nil
}

// verifyRendered holds an authored step carrying rendered values to want, the
// runbook's step: the same parameters; each one Rendered names templated in
// want, a key the emitter declares recordable for an undo through the step's
// method, which that method declares, holding a value the journal could have
// held and of the declared type, as verifyRecorded holds a recorded undo's;
// every other one exactly as written; and no templated one left unrendered.
// The planner asks it of each step it makes, and on the Walk tier the
// Controller and the Runner ask it again through Verify.
func verifyRendered(s Step, want engine.Task) error {
	label := fmt.Sprintf("step %d of node %s's rollback: list", s.Index, Printable(s.Node))
	spec, target, err := declaredUndo(s.Emitter, s.FQCN)
	if err != nil {
		return fmt.Errorf("%s carries rendered values: %w", label, err)
	}
	declared := fragment.ByNameIn(target)
	if len(s.Params) != len(want.Params) {
		return fmt.Errorf("%s is not what the runbook says", label)
	}
	for k, wv := range want.Params {
		v, ok := s.Params[k]
		if !ok {
			return fmt.Errorf("%s is not what the runbook says", label)
		}
		if !slices.Contains(s.Rendered, k) {
			if engine.HasTemplate(wv) {
				return fmt.Errorf("%s leaves its templated parameter %s unrendered beside rendered ones", label, Printable(k))
			}
			if !sameParams(map[string]any{k: wv}, map[string]any{k: v}) {
				return fmt.Errorf("%s is not what the runbook says", label)
			}
			continue
		}
		param, isDeclared := declared[k]
		switch {
		case !engine.HasTemplate(wv):
			return fmt.Errorf("%s carries a rendered value for %s, which its runbook writes as a literal", label, Printable(k))
		case collection.IsReservedParam(k) || !slices.Contains(spec.Record, k) || !isDeclared:
			return fmt.Errorf("%s carries a rendered value for %s, which %s does not declare recordable for an undo through %s", label, Printable(k), Printable(s.Emitter), Printable(s.FQCN))
		case !recordableValue(v, engine.ListParam(s.FQCN, k)):
			return fmt.Errorf("%s carries a rendered value for %s the journal would never hold", label, Printable(k))
		}
		if err := checkParamValue(k, v, param, s.FQCN); err != nil {
			return err
		}
	}
	return nil
}

// readsRunOnly reports whether task's params read anything but the device
// root: a root a rollback run has no value for. A template that does not
// compile counts, since nothing can say what it reads.
func readsRunOnly(task engine.Task) bool {
	eng := render.New()
	for _, source := range engine.TemplateStrings(task.Params) {
		tmpl, err := eng.Compile(source)
		if err != nil {
			return true
		}
		for _, expr := range tmpl.Expressions() {
			if len(expr.Path) == 0 || expr.Path[0] != engine.TemplateRootDevice {
				return true
			}
		}
	}
	return false
}
