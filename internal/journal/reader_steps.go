// Package journal: holding a read entry's rendered rollback: steps
// (engine.JournalEntry.AuthoredSteps) to what the engine writes.
package journal

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// authoredStepsProblem returns what is wrong with steps as a build writes
// them, or "": each at its own non-negative index, with a digest; a complete
// one carrying at least one value and no reason, an incomplete one a reason
// the engine records and no value; and every value one the journal may hold.
// The planner holds each value to the declarations again before replaying it.
func authoredStepsProblem(steps []engine.AuthoredStep) string {
	seen := map[int]bool{}
	for _, s := range steps {
		switch {
		case s.Index < 0 || seen[s.Index]:
			return fmt.Sprintf("step index %d is negative or repeated", s.Index)
		case s.FQCN == "":
			return fmt.Sprintf("step %d names no method", s.Index)
		case !digestPattern.MatchString(s.StepDigest):
			return fmt.Sprintf("step %d carries no step digest", s.Index)
		case s.Complete && (s.Withheld != "" || len(s.Params) == 0):
			return fmt.Sprintf("step %d is complete but carries a reason or no value", s.Index)
		case !s.Complete && (!engine.IsRecordedWithheldReason(s.Withheld) || len(s.Params) > 0):
			return fmt.Sprintf("step %d is incomplete with an unknown reason or with values", s.Index)
		}
		seen[s.Index] = true
		for _, p := range s.Params {
			if !holdableParam(p) {
				return fmt.Sprintf("step %d carries a value for %q the journal never holds", s.Index, p.Key)
			}
		}
	}
	return ""
}

// holdableParam reports whether p names a key and holds exactly one value of
// a kind the projection's recordedValue admits.
func holdableParam(p engine.InverseParam) bool {
	set := 0
	ok := p.Key != ""
	if p.Text != nil {
		set++
		ok = ok && engine.RecordableText(*p.Text)
	}
	if p.Texts != nil {
		set++
		_, listOK := engine.RecordableTexts(p.Texts)
		ok = ok && listOK
	}
	if p.Number != nil {
		set++
		_, err := p.Number.Float64()
		ok = ok && err == nil
	}
	if p.Bool != nil {
		set++
	}
	return ok && set == 1
}
