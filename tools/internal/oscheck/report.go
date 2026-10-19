// A check's record of its leg for a reader that is not a person: which
// machine, which commit, how it ended and where it stopped, and the records
// of the testgate runs on the machine, read back from it. pr-check's
// tools/prreport merges these with the Linux passes' records.
package oscheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// LegSchema is the version of LegRecord's shape.
const LegSchema = 1

// LegKind is the kind every LegRecord carries.
const LegKind = "leg"

// The steps a leg can stop at, in the order it takes them.
const (
	// StepSettings is a leg with no machine configured, which an optional
	// check skips: not a failure, but not a pass either.
	StepSettings  = "settings"
	StepBox       = "box"
	StepCommit    = "commit"
	StepReachable = "reachable"
	StepReady     = "ready"
	StepShip      = "ship"
	StepTest      = "test"
)

// LegRecord is one check's record of its machine's run.
type LegRecord struct {
	// Schema is the leg record's version (LegSchema).
	Schema int `json:"schema"`
	// Kind is always leg.
	Kind string `json:"kind"`
	// Tool is the check that ran the leg: maccheck, bsdcheck or wincheck.
	Tool string `json:"tool"`
	// OS is the machine's operating system, as the check names it.
	OS string `json:"os"`
	// Target is the machine: its device or ssh name.
	Target string `json:"target"`
	// Commit is the commit whose tree the leg tested.
	Commit string `json:"commit,omitempty"`
	// Packages are the import paths the leg tested.
	Packages []string `json:"packages"`
	// Started is when the leg began.
	Started time.Time `json:"started"`
	// Seconds is how long the leg took.
	Seconds float64 `json:"seconds"`
	// Verdict is flakegate's: pass, fail, not-run (the tests never ran),
	// or error (they ran and their outcome is unknown).
	Verdict string `json:"verdict"`
	// Step is where a leg that did not pass stopped.
	Step string `json:"step,omitempty"`
	// Error is why the leg did not pass, or what went wrong after its tests
	// passed (a box that could not be put back).
	Error string `json:"error,omitempty"`
	// Runs are the testgate records the machine wrote, in the order its
	// passes ran.
	Runs []flakegate.Run `json:"runs,omitempty"`
}

// RecordFiles are the files, in the unpacked tree, a machine's two testgate
// passes write their records to: the full pass, then the repeats.
var RecordFiles = []string{".pleiades-record-full.json", ".pleiades-record-repeat.json"}

// Recorder is a Leg that can read back the records its test run wrote.
type Recorder interface {
	// Records returns the testgate records on the machine for sha's tree,
	// those that exist, in the order RecordFiles names them.
	Records(ctx context.Context, sha string) ([]flakegate.Run, error)
}

// ParseRecords reads the testgate records a machine printed one after
// another, refusing any that is not one this reader knows.
func ParseRecords(out string) ([]flakegate.Run, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	var runs []flakegate.Run
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); errors.Is(err, io.EOF) {
			return runs, nil
		} else if err != nil {
			return runs, fmt.Errorf("reading the records the machine printed: %w", err)
		}
		r, err := flakegate.ReadRun(raw)
		if err != nil {
			return runs, err
		}
		runs = append(runs, r)
	}
}

// RecordNotConfigured writes, when o asks for a record, that the check had
// no machine configured, in words naming where one is set, and returns
// 0, or 2 when the record cannot be written.
func RecordNotConfigured(o *Options, tool, osName, why string, stderr io.Writer) int {
	if o.ReportOut == "" {
		return 0
	}
	rec := LegRecord{Tool: tool, OS: osName, Packages: o.Packages, Started: time.Now(),
		Verdict: flakegate.VerdictNotRun, Step: StepSettings, Error: why}
	if err := WriteLegRecord(o.ReportOut, rec); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", tool, err)
		return 2
	}
	return 0
}

// WriteLegRecord writes l to path, whole or not at all.
func WriteLegRecord(path string, l LegRecord) error {
	l.Schema, l.Kind = LegSchema, LegKind
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// writeFileAtomic writes data to path through a temporary file renamed over
// it, so a reader never sees half a record.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// ReadLegRecord reads a LegRecord, refusing one of another kind or schema.
func ReadLegRecord(data []byte) (LegRecord, error) {
	var l LegRecord
	if err := json.Unmarshal(data, &l); err != nil {
		return LegRecord{}, fmt.Errorf("reading a leg record: %w", err)
	}
	if l.Kind != LegKind || l.Schema != LegSchema {
		return LegRecord{}, fmt.Errorf("a record of kind %q, schema %d, is not a leg this reader knows", l.Kind, l.Schema)
	}
	return l, nil
}
