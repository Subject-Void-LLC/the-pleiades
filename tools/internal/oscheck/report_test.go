// Tests for a check's record of its leg: written for every way a run ends,
// with the machine's own testgate records read back, and the records a
// machine prints read one after another.
package oscheck

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// recordingLeg is a fakeLeg that can read its records back.
type recordingLeg struct {
	*fakeLeg
	runs []flakegate.Run
	err  error
}

func (r recordingLeg) Records(context.Context, string) ([]flakegate.Run, error) { return r.runs, r.err }

// readLegRecord reads the record at path, failing the test when there is
// none.
func readLegRecord(t *testing.T, path string) LegRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no record: %v", err)
	}
	rec, err := ReadLegRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestRun_WritesTheLegsRecord: each way a run ends leaves a record saying
// how and where, with the machine's records when the tests ran, and a
// prepare phase that succeeded leaves none for its test phase to write.
func TestRun_WritesTheLegsRecord(t *testing.T) {
	inRepo(t)
	full := flakegate.Run{Kind: flakegate.RunKind, Label: "testgate -full", Verdict: flakegate.VerdictPass}
	for name, tc := range map[string]struct {
		leg         Leg
		args        []string
		wantVerdict string
		wantStep    string
		wantRuns    int
		wantError   string
	}{
		"a pass":                  {recordingLeg{&fakeLeg{passed: true}, []flakegate.Run{full, full}, nil}, nil, flakegate.VerdictPass, "", 2, ""},
		"a failure":               {recordingLeg{&fakeLeg{}, []flakegate.Run{full}, nil}, nil, flakegate.VerdictFail, StepTest, 1, ""},
		"unreachable":             {&fakeLeg{reachable: errors.New("no route")}, nil, flakegate.VerdictNotRun, StepReachable, 0, "no route"},
		"not ready":               {&fakeLeg{ready: errors.New("no go")}, nil, flakegate.VerdictNotRun, StepReady, 0, "no go"},
		"not shipped":             {&fakeLeg{ship: errors.New("disk full")}, nil, flakegate.VerdictNotRun, StepShip, 0, "disk full"},
		"the tests could not run": {&fakeLeg{testErr: errors.New("connection reset")}, nil, flakegate.VerdictError, StepTest, 0, "connection reset"},
		"records unreadable":      {recordingLeg{&fakeLeg{passed: true}, nil, errors.New("no such file")}, nil, flakegate.VerdictPass, "", 0, "no such file"},
	} {
		out := filepath.Join(t.TempDir(), "leg.json")
		args := append(append([]string{"-report-out", out}, tc.args...), "a/b")
		code := flow.Run(context.Background(), options(t, args...), &bytes.Buffer{}, &bytes.Buffer{}, tc.leg)
		rec := readLegRecord(t, out)
		if rec.Verdict != tc.wantVerdict || rec.Step != tc.wantStep || len(rec.Runs) != tc.wantRuns || !strings.Contains(rec.Error, tc.wantError) {
			t.Errorf("%s: code %d, record %+v", name, code, rec)
		}
		if rec.Tool != "xcheck" || rec.OS != "BoxOS" || rec.Target != "box-1" || len(rec.Packages) != 1 || rec.Started.IsZero() {
			t.Errorf("%s: record header %+v", name, rec)
		}
		// The commit is resolved before the machine is asked anything.
		if rec.Commit == "" {
			t.Errorf("%s: the record names no commit", name)
		}
	}

	out := filepath.Join(t.TempDir(), "leg.json")
	if code := flow.Run(context.Background(), options(t, "-report-out", out, "-phase", "prepare", "a/b"), &bytes.Buffer{}, &bytes.Buffer{}, &fakeLeg{}); code != 0 {
		t.Fatalf("prepare: %d", code)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a prepare phase that succeeded wrote a record: %v", err)
	}

	if code := flow.Run(context.Background(), options(t, "-report-out", filepath.Join(t.TempDir(), "absent", "leg.json"), "a/b"), &bytes.Buffer{}, &bytes.Buffer{}, &fakeLeg{passed: true}); code != 2 {
		t.Errorf("a pass whose record could not be written returned %d", code)
	}
}

// TestRunWithBox_ABoxThatWillNotStartIsRecorded: the leg's record says it
// stopped at the box, with why.
func TestRunWithBox_ABoxThatWillNotStartIsRecorded(t *testing.T) {
	inRepo(t)
	h := &fakeHost{state: "saved", fail: map[string]error{"virt.vbox.vm.start": errors.New("the host is out of memory")}}
	h.install(t)
	out := filepath.Join(t.TempDir(), "leg.json")
	if code := runWithBox(context.Background(), options(t, "-report-out", out, "a/b"), flow, &fakeLeg{passed: true}, testBox, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatalf("code %d", code)
	}
	rec := readLegRecord(t, out)
	if rec.Verdict != flakegate.VerdictNotRun || rec.Step != StepBox || !strings.Contains(rec.Error, "out of memory") {
		t.Errorf("record %+v", rec)
	}
}

// TestRecordNotConfigured: an optional check with no machine leaves a
// record saying so, at the settings step; with no record asked for it
// writes nothing; and one it cannot write is a failure.
func TestRecordNotConfigured(t *testing.T) {
	out := filepath.Join(t.TempDir(), "leg.json")
	if code := RecordNotConfigured(options(t, "-report-out", out, "a/b"), "xcheck", "BoxOS", "no box configured", &bytes.Buffer{}); code != 0 {
		t.Fatalf("code %d", code)
	}
	rec := readLegRecord(t, out)
	if rec.Verdict != flakegate.VerdictNotRun || rec.Step != StepSettings || rec.Error != "no box configured" || rec.OS != "BoxOS" {
		t.Errorf("record %+v", rec)
	}
	if code := RecordNotConfigured(options(t, "a/b"), "xcheck", "BoxOS", "x", &bytes.Buffer{}); code != 0 {
		t.Errorf("no record asked for: %d", code)
	}
	if code := RecordNotConfigured(options(t, "-report-out", filepath.Join(t.TempDir(), "absent", "x.json"), "a/b"), "xcheck", "BoxOS", "x", &bytes.Buffer{}); code != 2 {
		t.Errorf("an unwritable record: %d", code)
	}
}

// TestParseRecords reads records one after another and refuses anything
// that is not one.
func TestParseRecords(t *testing.T) {
	a := `{"schema":1,"kind":"testgate","label":"full","verdict":"pass"}`
	b := `{"schema":1,"kind":"testgate","label":"repeat","verdict":"fail"}`
	runs, err := ParseRecords(a + "\n" + b + "\r\n")
	if err != nil || len(runs) != 2 || runs[1].Verdict != flakegate.VerdictFail {
		t.Fatalf("%v %+v", err, runs)
	}
	if runs, err := ParseRecords(""); err != nil || len(runs) != 0 {
		t.Errorf("nothing printed: %v %v", runs, err)
	}
	for name, out := range map[string]string{"not JSON": "Get-Content: no such file", "a leg": `{"schema":1,"kind":"leg"}`} {
		if _, err := ParseRecords(out); err == nil {
			t.Errorf("%s was read as a record", name)
		}
	}
}

// TestRecordsScript runs the POSIX records script against a home holding
// one record of the two: it prints that one, and nothing for the other.
func TestRecordsScript(t *testing.T) {
	needsSh(t)
	home := t.TempDir()
	tree := filepath.Join(home, ".b", "abc")
	if err := os.MkdirAll(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := flakegate.WriteRun(filepath.Join(tree, RecordFiles[0]), flakegate.Run{Label: "full", Verdict: flakegate.VerdictPass}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", POSIX{Dir: ".b"}.RecordsScript("abc"))
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	runs, err := ParseRecords(string(out))
	if err != nil || len(runs) != 1 || runs[0].Label != "full" {
		t.Errorf("%v %+v", err, runs)
	}
}
