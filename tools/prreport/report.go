// What a pr-check run's records add up to: each testgate pass on this
// machine, each OS check's leg, and each step pr-check ran besides, read
// from the records directory and merged into one document with one verdict.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// ReportSchema is the version of Report's shape.
const ReportSchema = 1

// ReportKind and StepKind are the kinds of the merged report and of a step
// record.
const (
	ReportKind = "pr-check"
	StepKind   = "step"
)

// reportFile is the merged report's name in the records directory, which a
// merge leaves out of what it reads.
const reportFile = "report.json"

// VerdictIncomplete is a report with no failure in it and a record it could
// not read: it cannot say the run passed.
const VerdictIncomplete = "incomplete"

// Step is the record of one step pr-check ran besides the tests (the
// coverage check, the served-images check): its name and how it exited, or
// why it did not run.
type Step struct {
	Schema   int    `json:"schema"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	ExitCode int    `json:"exit_code"`
	Verdict  string `json:"verdict"`
	// Reason is why a step with the not-run verdict did not run.
	Reason string `json:"reason,omitempty"`
}

// Pass is one testgate pass on this machine, by its record's name.
type Pass struct {
	Name string        `json:"name"`
	Run  flakegate.Run `json:"run"`
}

// Leg is one OS check's leg, by its record's name.
type Leg struct {
	Name string            `json:"name"`
	Leg  oscheck.LegRecord `json:"leg"`
}

// Report is a pr-check run's records, merged.
type Report struct {
	Schema    int       `json:"schema"`
	Kind      string    `json:"kind"`
	Commit    string    `json:"commit,omitempty"`
	Generated time.Time `json:"generated"`
	Verdict   string    `json:"verdict"`
	// ExitCode is pr-check's own exit status, the gate, when the merge was
	// given it. A report never passes a run the gate failed.
	ExitCode *int   `json:"exit_code,omitempty"`
	Passes   []Pass `json:"passes,omitempty"`
	Legs     []Leg  `json:"legs,omitempty"`
	Steps    []Step `json:"steps,omitempty"`
	// Problems are records that could not be read, each with why, and a
	// verdict that disagrees with pr-check's own status.
	Problems []string `json:"problems,omitempty"`
}

// writeStep writes a step's record to dir, named for it: how it exited, or
// with a reason, that it did not run.
func writeStep(dir, name string, exitCode int, notRun string) error {
	if !stepName(name) {
		return fmt.Errorf("%q is not a step name: letters, digits and - only", name)
	}
	s := Step{Schema: ReportSchema, Kind: StepKind, Name: name, ExitCode: exitCode, Verdict: flakegate.VerdictPass}
	switch {
	case notRun != "":
		s.Verdict, s.Reason = flakegate.VerdictNotRun, notRun
	case exitCode != 0:
		s.Verdict = flakegate.VerdictFail
	}
	return writeJSON(filepath.Join(dir, name+".json"), s)
}

// stepName reports whether name can name a step's file.
func stepName(name string) bool {
	if name == "" || name == "report" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// merge reads every record in dir but the merged report, by kind, and
// returns them as one report; a record it cannot read is a problem in it.
// exitCode, when not nil, is pr-check's own exit status.
func merge(dir, commit string, exitCode *int) (Report, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Report{}, fmt.Errorf("reading %s: %w", dir, err)
	}
	r := Report{Schema: ReportSchema, Kind: ReportKind, Commit: commit, Generated: time.Now().UTC(), ExitCode: exitCode}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" || name == reportFile {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- a file in the records directory this tool was pointed at
		if err != nil {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if err := r.add(strings.TrimSuffix(name, ".json"), data); err != nil {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: %v", name, err))
		}
	}
	sort.SliceStable(r.Passes, func(i, j int) bool { return order(r.Passes[i].Name) < order(r.Passes[j].Name) })
	sort.SliceStable(r.Legs, func(i, j int) bool { return order(r.Legs[i].Name) < order(r.Legs[j].Name) })
	sort.SliceStable(r.Steps, func(i, j int) bool { return order(r.Steps[i].Name) < order(r.Steps[j].Name) })
	r.Verdict = r.verdict()
	if exitCode != nil {
		switch {
		case *exitCode != 0 && r.Verdict != flakegate.VerdictFail:
			r.Problems = append(r.Problems, fmt.Sprintf("pr-check exited %d and no record says why: its own output does", *exitCode))
			r.Verdict = flakegate.VerdictFail
		case *exitCode == 0 && r.Verdict == flakegate.VerdictFail:
			r.Problems = append(r.Problems, "pr-check passed and a record failed: one of the two is wrong")
		}
	}
	return r, nil
}

// add reads one record into r by its kind.
func (r *Report) add(name string, data []byte) error {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("not a record: %w", err)
	}
	switch probe.Kind {
	case flakegate.RunKind:
		run, err := flakegate.ReadRun(data)
		if err != nil {
			return err
		}
		r.Passes = append(r.Passes, Pass{Name: name, Run: run})
	case oscheck.LegKind:
		leg, err := oscheck.ReadLegRecord(data)
		if err != nil {
			return err
		}
		r.Legs = append(r.Legs, Leg{Name: name, Leg: leg})
	case StepKind:
		var s Step
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s.Schema != ReportSchema {
			return fmt.Errorf("a step record of schema %d, where this reader knows %d", s.Schema, ReportSchema)
		}
		r.Steps = append(r.Steps, s)
	default:
		return fmt.Errorf("a record of kind %q, which this reader does not know", probe.Kind)
	}
	return nil
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

// verdict is fail when anything failed, could not finish, or never ran but
// for a check with no machine set up or a step that could not run here,
// each said and not failed; incomplete when nothing failed and a record
// could not be read; and pass otherwise.
func (r Report) verdict() string {
	for _, p := range r.Passes {
		if p.Run.Verdict != flakegate.VerdictPass {
			return flakegate.VerdictFail
		}
	}
	for _, l := range r.Legs {
		if l.Leg.Verdict != flakegate.VerdictPass && !notConfigured(l.Leg) {
			return flakegate.VerdictFail
		}
	}
	for _, s := range r.Steps {
		if s.Verdict != flakegate.VerdictPass && s.Verdict != flakegate.VerdictNotRun {
			return flakegate.VerdictFail
		}
	}
	if len(r.Problems) > 0 {
		return VerdictIncomplete
	}
	return flakegate.VerdictPass
}

// notConfigured reports whether l is a check that had no machine set up.
func notConfigured(l oscheck.LegRecord) bool {
	return l.Verdict == flakegate.VerdictNotRun && l.Step == oscheck.StepSettings
}

// readReport reads the merged report at path.
func readReport(path string) (Report, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the report this tool was pointed at
	if errors.Is(err, os.ErrNotExist) {
		return Report{}, fmt.Errorf("no report at %s: make pr-check writes one at the end of a run", path)
	}
	if err != nil {
		return Report{}, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if r.Kind != ReportKind || r.Schema != ReportSchema {
		return Report{}, fmt.Errorf("%s is a record of kind %q, schema %d, not a pr-check report this reader knows", path, r.Kind, r.Schema)
	}
	return r, nil
}

// writeJSON writes v to path through a temporary file renamed over it.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
