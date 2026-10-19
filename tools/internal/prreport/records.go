// Reading one record into a report: by its kind, refusing a kind or
// schema this reader does not know, and naming each value in it this
// reader does not know, so none is taken for a pass.
package prreport

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// add reads one record file into r by its kind, or records why it cannot.
func (r *Report) add(file string, data []byte) {
	name := strings.TrimSuffix(file, ".json")
	var probe struct {
		Kind   string `json:"kind"`
		Schema int    `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		r.problem(ProblemUnreadable, file, "not a record: "+err.Error())
		return
	}
	switch probe.Kind {
	case flakegate.RunKind:
		run, err := flakegate.ReadRun(data)
		if err != nil {
			r.problem(ProblemUnknownSchema, file, err.Error())
			return
		}
		r.Passes = append(r.Passes, Pass{Name: name, Run: run})
		r.checkRun(file, run)
	case oscheck.LegKind:
		leg, err := oscheck.ReadLegRecord(data)
		if err != nil {
			r.problem(ProblemUnknownSchema, file, err.Error())
			return
		}
		r.Legs = append(r.Legs, Leg{Name: name, Leg: leg})
		r.known(file, "verdict", leg.Verdict, KnownRunVerdicts)
		if leg.Step != "" {
			r.known(file, "step", leg.Step, KnownLegSteps)
		}
		for _, run := range leg.Runs {
			r.checkRun(file, run)
		}
	case StepKind:
		if probe.Schema != StepSchema {
			r.problem(ProblemUnknownSchema, file, fmt.Sprintf("a step record of schema %d, where this reader knows %d", probe.Schema, StepSchema))
			return
		}
		var s Step
		if err := json.Unmarshal(data, &s); err != nil {
			r.problem(ProblemUnreadable, file, err.Error())
			return
		}
		r.Steps = append(r.Steps, s)
		r.known(file, "verdict", s.Verdict, KnownStepVerdicts)
	default:
		r.problem(ProblemUnknownKind, file, fmt.Sprintf("a record of kind %q, which this reader does not know", probe.Kind))
	}
}

// checkRun records each value in run this reader does not know.
func (r *Report) checkRun(file string, run flakegate.Run) {
	r.known(file, "verdict", run.Verdict, KnownRunVerdicts)
	for _, f := range run.Failures {
		r.known(file, "outcome", f.Outcome, KnownOutcomes)
		r.known(file, "failure kind", f.Kind, KnownFailureKinds)
	}
}

// known records a problem when value is not one of known.
func (r *Report) known(file, what, value string, known []string) {
	if !slices.Contains(known, value) {
		r.problem(ProblemUnknownValue, file, fmt.Sprintf("the %s %q is not one this reader knows (%s), so it is not taken for a pass", what, value, strings.Join(known, ", ")))
	}
}
