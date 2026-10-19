// Tests for a testgate run's record: written and read back, and refused
// when it is not one this reader knows.
package flakegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunRecord_RoundTrips: a record written is read back whole, with its
// schema and kind set by the writer.
func TestRunRecord_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	in := Run{Label: "testgate -race", Verdict: VerdictFail, Packages: []string{"p"},
		Failures: []RunFailure{{Package: "p", Test: "T", Kind: "test", Outcome: OutcomeFailed, Output: "x_test.go:3: boom"}},
		Skips:    []Skip{{Package: "p", Test: "S", Reason: "needs docker"}}, Coverage: map[string]float64{"p": 81.5}}
	if err := WriteRun(path, in); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"reason": "needs docker"`) {
		t.Errorf("a skip's fields are not named in the record:\n%s", data)
	}
	out, err := ReadRun(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Schema != RunSchema || out.Kind != RunKind || out.Verdict != VerdictFail || out.Failures[0].Output != "x_test.go:3: boom" || out.Coverage["p"] != 81.5 {
		t.Errorf("read back %+v", out)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the temporary file was left behind: %v", err)
	}
}

// TestReadRun_RefusesWhatItDoesNotKnow: not JSON, another kind, another
// schema.
func TestReadRun_RefusesWhatItDoesNotKnow(t *testing.T) {
	for name, data := range map[string]string{
		"not JSON":       "{",
		"another kind":   `{"schema":1,"kind":"leg"}`,
		"another schema": `{"schema":2,"kind":"testgate"}`,
	} {
		if _, err := ReadRun([]byte(data)); err == nil {
			t.Errorf("%s was read as a record", name)
		}
	}
}

// TestRunFailures names each kind, carries the outcome and masks the
// output it takes from the events.
func TestRunFailures(t *testing.T) {
	events := []Event{{Action: "output", Package: "p", Test: "T", Output: "    x_test.go:3: token s3cret\n"}}
	got := RunFailures(events, []Failure{{Package: "p", Test: "T"}, {Package: "q", Kind: FailedBuild}}, OutcomeContention, 10,
		func(s string) string { return strings.ReplaceAll(s, "s3cret", "***") })
	if len(got) != 2 || got[0].Kind != "test" || got[1].Kind != "build" || got[0].Outcome != OutcomeContention {
		t.Fatalf("%+v", got)
	}
	if strings.Contains(got[0].Output, "s3cret") || !strings.Contains(got[0].Output, "token ***") {
		t.Errorf("output not masked: %q", got[0].Output)
	}
}
