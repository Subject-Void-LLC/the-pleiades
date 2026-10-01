// The skip ledger and the coverage reader, proved against a real
// `go test -json -cover` run of a throwaway module, because what they parse
// is the toolchain's own output and a hand-written event stream would only
// prove the hand was consistent with itself.
package flakegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// liveModule writes a throwaway module holding one package with code and
// tests that skip three different ways, and one package with nothing to
// instrument, then runs it through RunGoTestJSONPackages from inside the
// module, the way a gate runs the real one.
func liveModule(t *testing.T) []Event {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("go.mod", "module gateproof\n\ngo 1.22\n")
	write("cov/cov.go", `package cov

// Half reports 1 for true and 0 for false.
func Half(b bool) int {
	if b {
		return 1
	}
	return 0
}
`)
	write("cov/cov_test.go", `package cov

import "testing"

func TestHalf(t *testing.T) {
	if Half(true) != 1 {
		t.Fatal("wrong")
	}
}

func TestNeedsDocker(t *testing.T) { t.Skip("needs Docker: no daemon answered") }

func TestSkipNow(t *testing.T) { t.SkipNow() }

func TestBareSkip(t *testing.T) { t.Skip() }

func TestTable(t *testing.T) {
	t.Run("windows", func(t *testing.T) { t.Skipf("needs a real Windows host\nset PLEIADES_WINRM_HOST") })
	t.Run("linux", func(t *testing.T) {})
}
`)
	write("nostmt/nostmt_test.go", `package nostmt

import "testing"

func TestNothing(t *testing.T) {}
`)

	restore, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })

	events, err := RunGoTestJSONPackages([]string{"-cover", "-count=1"}, []string{"./..."}, nil)
	if err != nil {
		t.Fatalf("go test: %v", err)
	}
	return events
}

// TestSkips_ReadsEveryShapeOfSkip covers a skip with a message, a subtest's
// multi-line skip (its first line is the reason), and a skip with no
// message at all.
func TestSkips_ReadsEveryShapeOfSkip(t *testing.T) {
	skips := Skips(liveModule(t))
	got := map[string]string{}
	for _, s := range skips {
		if s.Package != "gateproof/cov" {
			t.Errorf("skip %+v is in the wrong package", s)
		}
		got[s.Test] = s.Reason
	}
	want := map[string]string{
		"TestNeedsDocker":   "needs Docker: no daemon answered",
		"TestSkipNow":       "no reason given",
		"TestBareSkip":      "no reason given",
		"TestTable/windows": "needs a real Windows host",
	}
	if len(got) != len(want) {
		t.Fatalf("skips = %+v, want exactly %v", skips, want)
	}
	for test, reason := range want {
		if got[test] != reason {
			t.Errorf("%s: reason = %q, want %q", test, got[test], reason)
		}
	}
}

// TestSkipLedger_GroupsByReason proves the ledger names every reason, counts
// each, and caps the tests it lists.
func TestSkipLedger_GroupsByReason(t *testing.T) {
	skips := []Skip{
		{"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec", "TestA", "needs a real Windows host"},
		{"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec", "TestB", "needs a real Windows host"},
		{"github.com/Subject-Void-LLC/the-pleiades/cmd/pleiades", "TestC", "needs a real Windows host"},
		{"github.com/Subject-Void-LLC/the-pleiades/tools/x", "TestD", "python3 is not installed"},
	}
	ledger := SkipLedger(skips, 2)
	for _, want := range []string{
		"4 test(s) skipped",
		"3: needs a real Windows host",
		"pkg/winrmexec TestA",
		"and 1 more",
		"1: python3 is not installed",
	} {
		if !strings.Contains(ledger, want) {
			t.Errorf("ledger lacks %q:\n%s", want, ledger)
		}
	}
	if strings.Index(ledger, "needs a real Windows host") > strings.Index(ledger, "python3") {
		t.Errorf("the largest group must come first:\n%s", ledger)
	}
	if SkipLedger(nil, 5) != "" {
		t.Error("an empty ledger must be empty")
	}
}

// TestCoverage_ReadsEachPackageFromItsOwnEvents proves the reader keys each
// number by the event's own package, records a real percentage, and leaves
// out a package with nothing to measure rather than calling it zero.
func TestCoverage_ReadsEachPackageFromItsOwnEvents(t *testing.T) {
	cov := Coverage(liveModule(t))
	if got, ok := cov["gateproof/cov"]; !ok || got < 66.6 || got > 66.7 {
		t.Errorf("gateproof/cov = %v, %v; want 66.7 (two of three statements)", got, ok)
	}
	if got, ok := cov["gateproof/nostmt"]; ok {
		t.Errorf("gateproof/nostmt = %v; a package with nothing to measure must be absent, not a number", got)
	}
}

// TestRunGoTestJSONPackages_RefusesAnEmptyList proves an empty selection is
// refused rather than handed to go test, which would test the current
// directory instead.
func TestRunGoTestJSONPackages_RefusesAnEmptyList(t *testing.T) {
	if _, err := RunGoTestJSONPackages(nil, nil, nil); err == nil {
		t.Fatal("an empty package list was accepted")
	}
}

// TestFailureOutput_ShowsWhyATestFailed proves a failing test's own
// message comes back, from a real run, and that a long output is cut to
// its end, where the failure is.
func TestFailureOutput_ShowsWhyATestFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gateproof\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := "package gateproof\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) {\n\tfor i := 0; i < 30; i++ {\n\t\tt.Log(\"noise\")\n\t}\n\tt.Fatal(\"the real reason\")\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	restore, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })
	events, _ := RunGoTestJSONPackages([]string{"-count=1"}, []string{"./..."}, nil)

	out := FailureOutput(events, Failure{Package: "gateproof", Test: "TestBroken"}, 5)
	if !strings.Contains(out, "the real reason") {
		t.Fatalf("output lacks the failure's own message:\n%s", out)
	}
	if !strings.Contains(out, "earlier line(s) left out") || strings.Count(out, "\n") > 5 {
		t.Fatalf("output was not cut to its last lines:\n%s", out)
	}
}
