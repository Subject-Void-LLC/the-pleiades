// Tests for the record testgate writes with -report-out: from real go test
// runs of the throwaway module, and the verdict rules on their own.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// readRecord reads the record at path.
func readRecord(t *testing.T, path string) flakegate.Run {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no record was written: %v", err)
	}
	rec, err := flakegate.ReadRun(data)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestRun_ARecordOfAPass: a passing run's record says pass, names its
// packages, lists both skips with their reasons and carries the coverage.
func TestRun_ARecordOfAPass(t *testing.T) {
	throwawayModule(t, false)
	out := filepath.Join(t.TempDir(), "run.json")
	if err := run(options{full: true, noRace: raceOff(t), strict: true, reportOut: out, packages: []string{"gateproof/good", "gateproof/skipper"}}); err != nil {
		t.Fatalf("run: %v", err)
	}
	rec := readRecord(t, out)
	if rec.Verdict != flakegate.VerdictPass || len(rec.Failures) != 0 || len(rec.Skips) != 2 || rec.Coverage["gateproof/good"] == 0 {
		t.Errorf("record = %+v", rec)
	}
	if !strings.HasPrefix(rec.Label, "testgate ") || len(rec.Packages) != 2 || rec.OS == "" || rec.Started.IsZero() {
		t.Errorf("record header = %q %v %q %v", rec.Label, rec.Packages, rec.OS, rec.Started)
	}
	if rec.Skips[0].Reason == "" {
		t.Errorf("a skip without its reason: %+v", rec.Skips[0])
	}
}

// TestRun_ARecordOfAFailure: a broken module's record says fail and holds
// the failing test with its own output, in a strict run and through the
// isolation pass, which confirms it alone.
func TestRun_ARecordOfAFailure(t *testing.T) {
	throwawayModule(t, true)
	for _, strict := range []bool{true, false} {
		out := filepath.Join(t.TempDir(), "run.json")
		err := run(options{full: true, noRace: raceOff(t), strict: strict, reportOut: out, packages: []string{"gateproof/good", "gateproof/bad"}})
		if err == nil {
			t.Fatalf("strict %v: a broken module passed", strict)
		}
		rec := readRecord(t, out)
		if rec.Verdict != flakegate.VerdictFail || rec.Error == "" || len(rec.Failures) != 1 {
			t.Fatalf("strict %v: record = %+v", strict, rec)
		}
		f := rec.Failures[0]
		if f.Package != "gateproof/bad" || f.Test != "TestBroken" || f.Kind != "test" || f.Outcome != flakegate.OutcomeFailed ||
			!strings.Contains(f.Output, "deliberately broken") {
			t.Errorf("strict %v: failure = %+v", strict, f)
		}
	}
}

// TestFinishRecord settles the verdict from how the run ended: pass, a
// failure that counts, an error with none, contention alone passing; and a
// record that cannot be written fails a run that passed.
func TestFinishRecord(t *testing.T) {
	dir := t.TempDir()
	contention := []flakegate.RunFailure{{Package: "p", Test: "T", Outcome: flakegate.OutcomeContention}}
	for name, tc := range map[string]struct {
		runErr   error
		failures []flakegate.RunFailure
		want     string
	}{
		"a pass":                  {nil, nil, flakegate.VerdictPass},
		"a pass with contention":  {nil, contention, flakegate.VerdictPass},
		"a failure":               {errors.New("1 failure(s)"), []flakegate.RunFailure{{Outcome: flakegate.OutcomeFailed}}, flakegate.VerdictFail},
		"too many to re-run":      {errors.New("too many"), []flakegate.RunFailure{{Outcome: flakegate.OutcomeNotRerun}}, flakegate.VerdictFail},
		"go test would not start": {errors.New("no output"), nil, flakegate.VerdictError},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		rec := &flakegate.Run{Started: time.Now(), Failures: tc.failures}
		if err := finishRecord(path, rec, tc.runErr); !errors.Is(err, tc.runErr) {
			t.Errorf("%s: returned %v, want the run's own %v", name, err, tc.runErr)
		}
		if got := readRecord(t, path); got.Verdict != tc.want || (tc.runErr != nil) != (got.Error != "") {
			t.Errorf("%s: verdict %q error %q, want %q", name, got.Verdict, got.Error, tc.want)
		}
	}

	if err := finishRecord("", &flakegate.Run{}, nil); err != nil {
		t.Errorf("no record asked for: %v", err)
	}
	unwritable := filepath.Join(dir, "absent", "run.json")
	if err := finishRecord(unwritable, &flakegate.Run{Started: time.Now()}, nil); err == nil {
		t.Error("a pass whose record could not be written was returned as a pass")
	}
	runErr := errors.New("the run's own")
	if err := finishRecord(unwritable, &flakegate.Run{Started: time.Now()}, runErr); !errors.Is(err, runErr) {
		t.Errorf("a failed run whose record could not be written returned %v", err)
	}
}
