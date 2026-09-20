// Package dispatch: the mode a job runs in, read off its launch fields.
package dispatch

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// Mode is the mode j runs in, read off its resolved launch fields
// (launch.ModeField, which launch resolution always sets for a kind that
// has a mode). A job without one is a real run: every job recorded before
// check mode existed, and every job of a kind that cannot be checked.
//
// A value that is not a mode is an error rather than a real run. The
// field is written by resolution, so a bad one means a record somebody
// altered, and the safe reading of an unreadable mode is not "execute":
// fan-out refuses to dispatch such a job, and a reader is told the mode
// could not be read rather than told it was a real run.
func (j *Job) Mode() (collection.Mode, error) {
	raw, set := j.Fields[launch.ModeField]
	if !set {
		return collection.ModeExecute, nil
	}
	s, _ := raw.(string)
	switch collection.Mode(s) {
	case collection.ModeExecute, collection.ModeCheck:
		return collection.Mode(s), nil
	default:
		return "", fmt.Errorf("job %s records mode %v, which is not a mode, so it was not dispatched", j.JobID, raw)
	}
}

// ModeLabel is Mode for a reader: "execute", "check", or "unreadable" for
// a record whose mode is not one, which is never shown as either of the
// two it might be mistaken for.
func (j *Job) ModeLabel() string {
	mode, err := j.Mode()
	if err != nil {
		return "unreadable"
	}
	return string(mode)
}

// CheckCoverage is what a check job covered, from its tasks: how many
// tasks its devices' checks could not check between them, and, once the
// job has finished, whether the check was complete.
//
// complete is true only when the job completed and every device it
// targeted was dispatched, reported success, and reported no unchecked
// task. A device skipped as not active was never checked, and a device
// whose check failed answered for nothing, so either leaves the check
// incomplete: whoever reads this, a person or Phase 48's gate, is asking
// whether the whole target was looked at, and anything short of that is
// no. decided is false while the job is still running, and for a job
// that is not a check, which has no coverage to report.
func (j *Job) CheckCoverage(tasks []JobTask) (complete, decided bool, unchecked int) {
	if mode, err := j.Mode(); err != nil || mode != collection.ModeCheck {
		return false, false, 0
	}
	complete = j.State == "completed"
	for _, t := range tasks {
		unchecked += t.Unchecked
		if t.Outcome != OutcomeDispatched || t.Result != ResultSucceeded || t.Unchecked > 0 {
			complete = false
		}
	}
	switch j.State {
	case "completed", "failed", "canceled":
		return complete, true, unchecked
	default:
		return false, false, unchecked
	}
}
