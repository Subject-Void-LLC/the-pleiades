// The ledger's entry: one failure, contention or leg outcome from one run,
// as one JSON object on one line. It is a versioned contract (EntrySchema):
// a reader refuses a major version it does not know, ignores a field it does
// not know, and shows a verdict or kind it does not know as it is.
package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// EntrySchema is the entry's major version. It goes up only for a change an
// existing reader could misread: a field removed or renamed, or given another
// type or meaning. A newer producer of the same version may add fields and
// values.
const EntrySchema = 1

// The two sources, which are also the two ledgers.
const (
	// SourcePRCheck is a local make pr-check run.
	SourcePRCheck = "pr-check"
	// SourceActions is a GitHub Actions job.
	SourceActions = "actions"
)

// The two kinds of entry.
const (
	// KindTest is a test or package that failed in a testgate pass.
	KindTest = "test"
	// KindLeg is a leg, job or step that failed or did not run, with no test
	// to blame.
	KindLeg = "leg"
)

// The verdicts a test entry carries.
const (
	// VerdictReal failed again when re-run alone.
	VerdictReal = "real"
	// VerdictContention passed when re-run alone, so it lost a race; a test
	// called this run after run needs a look, since a real concurrency bug
	// looks the same.
	VerdictContention = "contention"
	// VerdictStrict failed in a -strict pass, which re-runs nothing, so it is
	// not known whether it would pass alone.
	VerdictStrict = "strict"
	// VerdictNotRerun failed among too many failures to re-run any. Actions
	// logs print these under the same heading as real failures, so only a
	// pr-check record tells the two apart.
	VerdictNotRerun = "not-rerun"
)

// The verdicts a leg entry carries.
const (
	// VerdictNotRun is a leg or step that never ran, or stopped before its
	// tests did.
	VerdictNotRun = "not-run"
	// VerdictFail is a leg or step that ran and failed with no test to blame.
	VerdictFail = "fail"
)

// The known values, by field. A value outside them is shown as it is and
// never dropped.
var (
	KnownSources      = []string{SourcePRCheck, SourceActions}
	KnownKinds        = []string{KindTest, KindLeg}
	KnownTestVerdicts = []string{VerdictReal, VerdictContention, VerdictStrict, VerdictNotRerun}
	KnownLegVerdicts  = []string{VerdictNotRun, VerdictFail}
)

// Entry is one line of a ledger. Every free-text field (error, reason, note)
// has been through scrub before it is stored: masked by the shared ruleset,
// with user@host names, IP addresses and home directories replaced by
// placeholders, and capped. A host name or test output body is never stored.
type Entry struct {
	// Schema is the entry's major version (EntrySchema).
	Schema int `json:"schema"`
	// Source is pr-check or actions.
	Source string `json:"source"`
	// RecordedAt is when this entry was written to the ledger.
	RecordedAt time.Time `json:"recorded_at"`
	// SeenAt is when the run or job happened: a pr-check run's start, or a
	// job's completion.
	SeenAt time.Time `json:"seen_at"`
	// Commit is the full SHA the run checked.
	Commit string `json:"commit"`
	// Branch is the branch the commit was on, when known.
	Branch string `json:"branch,omitempty"`
	// Run is a pr-check run's id: its start time and commit.
	Run string `json:"run,omitempty"`
	// RunID is an Actions workflow run's id.
	RunID int64 `json:"run_id,omitempty"`
	// JobID is an Actions job's id.
	JobID int64 `json:"job_id,omitempty"`
	// Kind is test or leg.
	Kind string `json:"kind"`
	// Leg is where it happened: a pr-check pass, leg or step (fast, repeat,
	// containers, mac, bsd, win, coverage, ...), or an Actions job's name.
	Leg string `json:"leg"`
	// Pass is, on a pr-check machine leg, which of its passes failed: full or
	// repeat.
	Pass string `json:"pass,omitempty"`
	// OS is the operating system it ran on: linux, macOS, FreeBSD, Windows.
	OS string `json:"os,omitempty"`
	// Package is a test entry's import path; absent on a leg entry.
	Package string `json:"package,omitempty"`
	// Test is a test entry's test, absent for a package-level failure, whose
	// reason then says what failed.
	Test string `json:"test,omitempty"`
	// Verdict is real, contention, strict or not-rerun for a test entry, and
	// not-run or fail for a leg entry.
	Verdict string `json:"verdict"`
	// Reason is why a leg did not pass, or what a package-level failure was.
	Reason string `json:"reason,omitempty"`
	// Error is the first line of a test's own output that says why it failed.
	Error string `json:"error,omitempty"`
	// Pattern is the FAILURE_PATTERNS number annotate gave this entry.
	Pattern int `json:"pattern,omitempty"`
	// Note is the note annotate gave this entry.
	Note string `json:"note,omitempty"`
}

// key is what makes two entries the same, so a run recorded twice appends
// nothing the second time.
func (e Entry) key() string {
	return strings.Join([]string{e.Source, e.Commit, e.Leg, e.Pass, e.Package, e.Test, e.Verdict, e.Run,
		strconv.FormatInt(e.RunID, 10), strconv.FormatInt(e.JobID, 10)}, "\x00")
}

// name is a test entry's package and test as one string, the unit the report
// counts by: the package alone for a package-level failure.
func (e Entry) name() string {
	if e.Test == "" {
		return e.Package
	}
	return e.Package + "." + e.Test
}

// check refuses an entry this reader cannot read as one: another major
// version, or one missing what every entry has. A verdict or kind it does
// not know is not refused, since a newer producer may add one.
func (e Entry) check() error {
	if e.Schema != EntrySchema {
		return fmt.Errorf("an entry of schema %d, where this reader knows %d: read it with the tools of the commit that wrote it", e.Schema, EntrySchema)
	}
	if e.Source == "" || e.Kind == "" || e.Leg == "" || e.Verdict == "" || e.Commit == "" {
		return fmt.Errorf("an entry with no source, kind, leg, verdict or commit")
	}
	return nil
}

// known reports whether value is one of the values this reader knows.
func known(value string, values []string) bool {
	return slices.Contains(values, value)
}
