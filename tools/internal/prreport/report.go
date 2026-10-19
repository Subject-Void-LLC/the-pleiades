// Package prreport is the pr-check report: a run's records merged into one
// document with one verdict, and the contract a tool reading that document
// relies on. tools/prreport writes it and prints it for a person; a
// dashboard or an agent reads the same JSON. tools/gendocs publishes its
// JSON Schema (docs/reference/schemas/pr-check-report.json) from these
// types and their comments, so the schema cannot drift from the code.
//
// # The contract
//
// Schema is the major version. It goes up only for a change an existing
// reader could misread: a field removed or renamed, or given another type or
// meaning. Within a major version a newer producer may add fields, and new
// values to the open sets (verdicts, outcomes, steps, problem codes). So a
// reader:
//   - ignores a field it does not know;
//   - treats a verdict, outcome or step it does not know as not a pass, and
//     shows it as it is;
//   - refuses a major version it does not know, rather than guessing.
//
// The verdict is never pass unless every expected part of the run has a
// record, every record was read, every value in them is known, and
// pr-check's own exit status, when given, was 0.
package prreport

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// ReportSchema is the report's major version. 2 made each problem an object
// with a code (it was a sentence) and added started, expected and missing.
const ReportSchema = 2

// StepSchema is the version of a step's record.
const StepSchema = 1

// ReportKind and StepKind are the kinds of the merged report and of a step
// record.
const (
	ReportKind = "pr-check"
	StepKind   = "step"
)

// ReportFile is the merged report's name in the records directory, which a
// merge leaves out of what it reads.
const ReportFile = "report.json"

// The report's own verdicts, beside flakegate's pass and fail.
const (
	// VerdictIncomplete is a run with nothing against it that cannot be
	// called a pass: an expected record is missing, a record could not be
	// read, or one holds a value this reader does not know.
	VerdictIncomplete = "incomplete"
	// VerdictRunning is a report written as pr-check began: not a result.
	// One still saying so after its run is gone is an abandoned run.
	VerdictRunning = "running"
	// VerdictInterrupted is a run that stopped before its end, by a signal or
	// an early exit: what ran is recorded, and what did not is missing.
	VerdictInterrupted = "interrupted"
)

// The problem codes, stable for a tool to act on; each problem's detail is
// for a person.
const (
	// ProblemUnreadable is a record that could not be read or is not JSON.
	ProblemUnreadable = "unreadable"
	// ProblemUnknownKind is a record of a kind this reader does not know.
	ProblemUnknownKind = "unknown-kind"
	// ProblemUnknownSchema is a record of a schema this reader does not know.
	ProblemUnknownSchema = "unknown-schema"
	// ProblemUnknownValue is a verdict, outcome, kind or step this reader
	// does not know, in a record it otherwise read.
	ProblemUnknownValue = "unknown-value"
	// ProblemMissing is a part of the run pr-check said it would run, with
	// no record.
	ProblemMissing = "missing"
	// ProblemGateFailed is pr-check failing with no record saying why.
	ProblemGateFailed = "gate-failed"
	// ProblemGateDisagrees is pr-check passing over a record that failed.
	ProblemGateDisagrees = "gate-disagrees"
	// ProblemInterrupted is a run that stopped before its end.
	ProblemInterrupted = "interrupted"
)

// The values this reader knows, by where they appear. A value outside them
// is a problem (ProblemUnknownValue), never a pass.
var (
	KnownReportVerdicts = []string{flakegate.VerdictPass, flakegate.VerdictFail, VerdictIncomplete, VerdictRunning, VerdictInterrupted}
	KnownRunVerdicts    = []string{flakegate.VerdictPass, flakegate.VerdictFail, flakegate.VerdictError, flakegate.VerdictNotRun}
	KnownOutcomes       = []string{flakegate.OutcomeFailed, flakegate.OutcomeContention, flakegate.OutcomeNotRerun}
	KnownFailureKinds   = []string{"test", "build", "package"}
	KnownStepVerdicts   = []string{flakegate.VerdictPass, flakegate.VerdictFail, flakegate.VerdictNotRun}
	KnownLegSteps       = []string{oscheck.StepSettings, oscheck.StepBox, oscheck.StepCommit, oscheck.StepReachable, oscheck.StepReady, oscheck.StepShip, oscheck.StepTest}
	KnownProblemCodes   = []string{ProblemUnreadable, ProblemUnknownKind, ProblemUnknownSchema, ProblemUnknownValue, ProblemMissing, ProblemGateFailed, ProblemGateDisagrees, ProblemInterrupted}
)

// Step is the record of one step pr-check ran besides the tests (the
// coverage check, the served-images check): how it exited, or why it did not
// run.
type Step struct {
	// Schema is the step record's version (StepSchema).
	Schema int `json:"schema"`
	// Kind is always step.
	Kind string `json:"kind"`
	// Name is the step's name, which is also its record's file name.
	Name string `json:"name"`
	// ExitCode is how the step's command exited; 0 for a step that did not
	// run.
	ExitCode int `json:"exit_code"`
	// Verdict is pass, fail, or not-run for a step this machine could not
	// make, which is said and not failed.
	Verdict string `json:"verdict"`
	// Reason is why a step with the not-run verdict did not run.
	Reason string `json:"reason,omitempty"`
}

// Pass is one testgate pass on this machine, by its record's name.
type Pass struct {
	// Name is the pass's record name: fast, repeat or containers.
	Name string `json:"name"`
	// Run is the pass's own testgate record.
	Run flakegate.Run `json:"run"`
}

// Leg is one OS check's leg, by its record's name.
type Leg struct {
	// Name is the leg's record name: mac, bsd or win.
	Name string `json:"name"`
	// Leg is the check's own record of its leg.
	Leg oscheck.LegRecord `json:"leg"`
}

// Problem is one reason a report cannot simply be read as its records say.
type Problem struct {
	// Code is what kind of problem it is, stable for a tool to act on.
	Code string `json:"code"`
	// Record is the record file or the expected part of the run it is about;
	// absent for a problem about the run as a whole.
	Record string `json:"record,omitempty"`
	// Detail says what happened, for a person.
	Detail string `json:"detail"`
}

// Report is a pr-check run's records merged into one document with one
// verdict. Compatibility: schema is the major version, raised only by a
// change an existing reader could misread (a field removed or renamed, or
// given another type or meaning). Within a major version a newer producer
// may add fields, and values to the open sets; a reader ignores a field it
// does not know, treats a verdict, outcome or step it does not know as not a
// pass, and refuses a major version it does not know.
type Report struct {
	// Schema is the report's major version.
	Schema int `json:"schema"`
	// Kind is always pr-check.
	Kind string `json:"kind"`
	// Commit is the commit the run checked.
	Commit string `json:"commit,omitempty"`
	// Started is when pr-check began; absent when it wrote no running report
	// first.
	Started *time.Time `json:"started,omitempty"`
	// Generated is when this document was written.
	Generated time.Time `json:"generated"`
	// Verdict is the run's: pass, fail, incomplete (nothing failed, and
	// something cannot be read as a pass), running (pr-check has begun and not
	// ended: not a result) or interrupted (it stopped before its end).
	Verdict string `json:"verdict"`
	// ExitCode is pr-check's own exit status, the gate, when the merge was
	// given it. A report never passes a run the gate failed.
	ExitCode *int `json:"exit_code,omitempty"`
	// Expected is every part of the run pr-check said, as it began, it would
	// run: passes, legs and steps by name.
	Expected []string `json:"expected,omitempty"`
	// Missing is each expected part with no record, which is never a pass.
	Missing []string `json:"missing,omitempty"`
	// Passes are the testgate passes on this machine, in the order pr-check
	// runs them.
	Passes []Pass `json:"passes,omitempty"`
	// Legs are the OS checks' legs, in the order pr-check runs them.
	Legs []Leg `json:"legs,omitempty"`
	// Steps are the other steps, in the order pr-check runs them.
	Steps []Step `json:"steps,omitempty"`
	// Problems are each reason the report cannot simply be read as its
	// records say.
	Problems []Problem `json:"problems,omitempty"`
}

// ValidName reports whether name can name a record: letters, digits and -,
// and not the report's own.
func ValidName(name string) bool {
	if name == "" || name+".json" == ReportFile {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// WriteStep writes a step's record to dir, named for it: how it exited, or
// with a reason, that it did not run.
func WriteStep(dir, name string, exitCode int, notRun string) error {
	if !ValidName(name) {
		return fmt.Errorf("%q is not a step name: letters, digits and - only", name)
	}
	s := Step{Schema: StepSchema, Kind: StepKind, Name: name, ExitCode: exitCode, Verdict: flakegate.VerdictPass}
	switch {
	case notRun != "":
		s.Verdict, s.Reason = flakegate.VerdictNotRun, notRun
	case exitCode != 0:
		s.Verdict = flakegate.VerdictFail
	}
	return WriteJSON(filepath.Join(dir, name+".json"), s)
}

// Begin writes a running report to dir as pr-check starts: when, for which
// commit, and what it expects to run. Until the merge replaces it, it says
// the run has not ended, and a merge that never comes leaves it saying so.
func Begin(dir, commit string, expected []string, now time.Time) (Report, error) {
	for _, name := range expected {
		if !ValidName(name) {
			return Report{}, fmt.Errorf("%q is not a record name: letters, digits and - only", name)
		}
	}
	started := now.UTC()
	r := Report{Schema: ReportSchema, Kind: ReportKind, Commit: commit, Started: &started, Generated: started,
		Verdict: VerdictRunning, Expected: expected}
	return r, WriteJSON(filepath.Join(dir, ReportFile), r)
}

// MergeOptions is what a merge is told besides the records.
type MergeOptions struct {
	// Commit is the commit the run checked.
	Commit string
	// ExitCode is pr-check's own exit status, nil when not given.
	ExitCode *int
	// Interrupted says the run stopped before its end.
	Interrupted bool
	// Now is the time the report is written.
	Now time.Time
}

// Merge reads every record in dir but the report, by kind, and returns them
// as one report. A running report already there (Begin's) gives the start
// time and what was expected; a record that cannot be read, or holds a
// value this reader does not know, is a problem in it.
func Merge(dir string, o MergeOptions) (Report, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Report{}, fmt.Errorf("reading %s: %w", dir, err)
	}
	r := Report{Schema: ReportSchema, Kind: ReportKind, Commit: o.Commit, Generated: o.Now.UTC(), ExitCode: o.ExitCode}
	if began, err := ReadReport(filepath.Join(dir, ReportFile)); err == nil && began.Verdict == VerdictRunning {
		r.Started, r.Expected = began.Started, began.Expected
		if r.Commit == "" {
			r.Commit = began.Commit
		}
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" || name == ReportFile {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- a file in the records directory this tool was pointed at
		if err != nil {
			r.problem(ProblemUnreadable, name, err.Error())
			continue
		}
		r.add(name, data)
	}
	sort.SliceStable(r.Passes, func(i, j int) bool { return order(r.Passes[i].Name) < order(r.Passes[j].Name) })
	sort.SliceStable(r.Legs, func(i, j int) bool { return order(r.Legs[i].Name) < order(r.Legs[j].Name) })
	sort.SliceStable(r.Steps, func(i, j int) bool { return order(r.Steps[i].Name) < order(r.Steps[j].Name) })
	for _, name := range r.Expected {
		if !r.has(name) {
			r.Missing = append(r.Missing, name)
			r.problem(ProblemMissing, name, "pr-check said it would run "+name+", and there is no record of it")
		}
	}
	r.Verdict = r.verdict(o.Interrupted)
	if o.Interrupted {
		r.problem(ProblemInterrupted, "", "pr-check stopped before it finished: what ran is recorded, and what did not is missing")
	}
	if o.ExitCode != nil {
		switch {
		case *o.ExitCode != 0 && r.Verdict != flakegate.VerdictFail && r.Verdict != VerdictInterrupted:
			r.problem(ProblemGateFailed, "", fmt.Sprintf("pr-check exited %d and no record says why: its own output does", *o.ExitCode))
			r.Verdict = flakegate.VerdictFail
		case *o.ExitCode == 0 && r.Verdict == flakegate.VerdictFail:
			r.problem(ProblemGateDisagrees, "", "pr-check passed and a record failed: one of the two is wrong")
		}
	}
	return r, nil
}

// problem adds one problem to r.
func (r *Report) problem(code, record, detail string) {
	r.Problems = append(r.Problems, Problem{Code: code, Record: record, Detail: detail})
}

// has reports whether r holds a pass, leg or step called name.
func (r Report) has(name string) bool {
	for _, p := range r.Passes {
		if p.Name == name {
			return true
		}
	}
	for _, l := range r.Legs {
		if l.Name == name {
			return true
		}
	}
	for _, s := range r.Steps {
		if s.Name == name {
			return true
		}
	}
	return false
}

// orderOf ranks the names pr-check gives its records in the order it runs
// them; any other name follows, alphabetically.
var orderOf = map[string]int{"fast": 1, "repeat": 2, "containers": 3, "mac": 4, "bsd": 5, "win": 6, "images": 7, "coverage": 8, "durations": 9}

// order is a name's sort key.
func order(name string) string {
	if n, ok := orderOf[name]; ok {
		return fmt.Sprintf("%02d", n)
	}
	return "99" + name
}

// verdict is interrupted for a run that stopped early; fail when anything
// failed, could not finish, or never ran (but for a check with no machine
// set up or a step that could not run here, each said and not failed);
// incomplete when nothing failed and the report has a problem; and pass
// otherwise.
func (r Report) verdict(interrupted bool) string {
	switch {
	case interrupted:
		return VerdictInterrupted
	case r.failed():
		return flakegate.VerdictFail
	case len(r.Problems) > 0:
		return VerdictIncomplete
	}
	return flakegate.VerdictPass
}

// failed reports whether any pass, leg or step says, in a value this reader
// knows, that it failed or did not finish.
func (r Report) failed() bool {
	bad := []string{flakegate.VerdictFail, flakegate.VerdictError, flakegate.VerdictNotRun}
	for _, p := range r.Passes {
		if slices.Contains(bad, p.Run.Verdict) {
			return true
		}
	}
	for _, l := range r.Legs {
		if slices.Contains(bad, l.Leg.Verdict) && !NotConfigured(l.Leg) {
			return true
		}
	}
	for _, s := range r.Steps {
		if s.Verdict == flakegate.VerdictFail {
			return true
		}
	}
	return false
}

// NotConfigured reports whether l is a check that had no machine set up.
func NotConfigured(l oscheck.LegRecord) bool {
	return l.Verdict == flakegate.VerdictNotRun && l.Step == oscheck.StepSettings
}
