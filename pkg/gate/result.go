// The typed result each check yields, and a run's summary of them.
package gate

import (
	"fmt"
	"strings"
	"time"
)

// Status is a check's outcome.
type Status string

// The three outcomes. A SKIP is reported as a SKIP: nothing turns one into
// a PASS.
const (
	Pass Status = "PASS"
	Fail Status = "FAIL"
	Skip Status = "SKIP"
)

// NoExit is Result.ExitCode for a check that did not run to an exit status.
const NoExit = -1

// Result is what one check yielded.
type Result struct {
	// Name and Kind are the check's.
	Name string
	Kind string
	// Status is PASS, FAIL or SKIP.
	Status Status
	// Reason says why, for anything but a plain PASS.
	Reason string
	// Duration is how long the check ran; zero for one that never started.
	Duration time.Duration
	// ExitCode is the check's exit status, or NoExit.
	ExitCode int
	// Tail is the end of a failing or skipping check's output, bounded and
	// escaped (Tail); empty for a pass.
	Tail string
}

// Map is r as a result value a task records: plain types only, so it
// crosses a process boundary as JSON unchanged.
func (r Result) Map() map[string]any {
	return map[string]any{
		"name":        r.Name,
		"kind":        r.Kind,
		"status":      string(r.Status),
		"reason":      r.Reason,
		"duration_ms": r.Duration.Milliseconds(),
		"exit_code":   r.ExitCode,
		"tail":        r.Tail,
	}
}

// Skipped is a result for a check that did not run, with reason.
func Skipped(c Check, reason string) Result {
	return Result{Name: c.Name, Kind: c.Kind, Status: Skip, Reason: reason, ExitCode: NoExit}
}

// Summary counts a run's results.
type Summary struct {
	Passed, Failed, Skipped int
}

// Summarize counts results.
func Summarize(results []Result) Summary {
	var s Summary
	for _, r := range results {
		switch r.Status {
		case Pass:
			s.Passed++
		case Fail:
			s.Failed++
		default:
			s.Skipped++
		}
	}
	return s
}

// String is s for a person: "3 passed, 1 failed, 2 skipped".
func (s Summary) String() string {
	return fmt.Sprintf("%d passed, %d failed, %d skipped", s.Passed, s.Failed, s.Skipped)
}

// Verdict is the error a run ends with, or nil: any FAIL fails it, and so
// does any SKIP when failOnSkip is set. The error names each check that
// failed it.
func Verdict(results []Result, failOnSkip bool) error {
	var failed, skipped []string
	for _, r := range results {
		switch r.Status {
		case Fail:
			failed = append(failed, r.Name)
		case Skip:
			skipped = append(skipped, r.Name)
		}
	}
	s := Summarize(results)
	switch {
	case len(failed) > 0:
		return fmt.Errorf("the gate failed (%s): %s", s, strings.Join(failed, ", "))
	case failOnSkip && len(skipped) > 0:
		return fmt.Errorf("the gate skipped checks and fail_on_skip is set (%s): %s", s, strings.Join(skipped, ", "))
	}
	return nil
}
