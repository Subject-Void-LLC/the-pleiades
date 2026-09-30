// testgate's verdicts, proved against real `go test` runs of a throwaway
// module, since the claim is about what the gate does with the toolchain's
// real output.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// throwawayModule writes a module with a package that passes and measures
// coverage, one that skips, and, when broken is set, one whose test always
// fails, then changes into it for the test's duration. It writes an empty
// flaky-packages.json, as testgate reads one from the working directory.
func throwawayModule(t *testing.T, broken bool) {
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
			t.Fatal(err)
		}
	}
	write("go.mod", "module gateproof\n\ngo 1.22\n")
	write("flaky-packages.json", "{}\n")
	write("good/good.go", "package good\n\n// One returns 1.\nfunc One() int { return 1 }\n")
	write("good/good_test.go", "package good\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {\n\tif One() != 1 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n")
	write("skipper/skipper_test.go", "package skipper\n\nimport \"testing\"\n\nfunc TestHost(t *testing.T) { t.Skip(\"needs a real Windows host\") }\n")
	if broken {
		write("bad/bad_test.go", "package bad\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"deliberately broken\") }\n")
	}
	restore, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })
}

// TestRun_TheOnePassMeasuresAndReports runs -full over a passing module:
// it passes, writes each measured package's coverage, and writes the skip
// to the job summary when one is set.
func TestRun_TheOnePassMeasuresAndReports(t *testing.T) {
	throwawayModule(t, false)
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	coverage := filepath.Join(t.TempDir(), "coverage.json")

	err := run(options{full: true, strict: true, coverageOut: coverage, packages: []string{"gateproof/good", "gateproof/skipper"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	raw, err := os.ReadFile(coverage)
	if err != nil {
		t.Fatal(err)
	}
	var measured map[string]float64
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	if measured["gateproof/good"] != 100 {
		t.Errorf("coverage = %v, want gateproof/good at 100", measured)
	}
	page, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "needs a real Windows host") || !strings.Contains(string(page), "skipper TestHost") {
		t.Errorf("the job summary does not report the skip:\n%s", page)
	}
}

// TestRun_ABrokenTestFailsBothWays proves a test that fails every time
// fails the gate strict and tolerant alike: strict never re-runs it, and
// the tolerant re-run alone confirms it rather than excusing it.
func TestRun_ABrokenTestFailsBothWays(t *testing.T) {
	throwawayModule(t, true)
	packages := []string{"gateproof/good", "gateproof/bad"}
	if err := run(options{full: true, strict: true, packages: packages}); err == nil {
		t.Error("strict: a broken test passed the gate")
	}
	if err := run(options{full: true, packages: packages}); err == nil || !strings.Contains(err.Error(), "confirmed in isolation") {
		t.Errorf("tolerant: err = %v, want the failure confirmed when re-run alone", err)
	}
}

// TestRun_ShardsTogetherTestEverything proves two shards of a list test its
// packages between them, each once.
func TestRun_ShardsTogetherTestEverything(t *testing.T) {
	throwawayModule(t, false)
	dir := t.TempDir()
	packages := []string{"gateproof/good", "gateproof/skipper"}
	for i, shard := range []string{"1/2", "2/2"} {
		out := filepath.Join(dir, "c"+string(rune('0'+i))+".json")
		if err := run(options{full: true, strict: true, shard: shard, coverageOut: out, packages: packages}); err != nil {
			t.Fatalf("shard %s: %v", shard, err)
		}
	}
	first, _ := os.ReadFile(filepath.Join(dir, "c0.json"))
	if !strings.Contains(string(first), "gateproof/good") {
		t.Errorf("shard 1/2 did not measure gateproof/good: %s", first)
	}
}

// TestRun_ContentionIsReMeasuredAlone proves a package whose failure was
// contention gets its coverage from a whole run alone, not from the run
// where its test stopped partway.
//
// The test fails the first time it runs, as a contended one would, before
// covering half the package, and passes every time after. The first run
// therefore measures 50%; the gate must record the 100% its re-run alone
// measures, or a floor check reads contention as a coverage drop, which is
// what internal/ent/migrate's did on 2026-09-30.
func TestRun_ContentionIsReMeasuredAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}
	dir := t.TempDir()
	t.Setenv("GATEPROOF_DIR", dir)
	files := map[string]string{
		"go.mod":              "module gateproof\n\ngo 1.22\n",
		"flaky-packages.json": "{}\n",
		"cov/cov.go":          "package cov\n\n// Early returns 1.\nfunc Early() int { return 1 }\n\n// Late returns 2.\nfunc Late() int { return 2 }\n",
		"cov/cov_test.go": `package cov

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstRunFails(t *testing.T) {
	Early()
	marker := filepath.Join(os.Getenv("GATEPROOF_DIR"), "ran-once")
	if _, err := os.Stat(marker); err != nil {
		_ = os.WriteFile(marker, nil, 0o600)
		t.Fatal("the first run fails, as a contended one would")
	}
	Late()
}
`,
	}
	module := filepath.Join(dir, "module")
	for name, body := range files {
		path := filepath.Join(module, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	restore, _ := os.Getwd()
	if err := os.Chdir(module); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })

	summary := filepath.Join(dir, "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	out := filepath.Join(dir, "coverage.json")
	if err := run(options{full: true, coverageOut: out, packages: []string{"gateproof/cov"}}); err != nil {
		t.Fatalf("run: %v; a failure that passes alone is contention", err)
	}
	// The job summary carries the tolerated failure's own output, which is
	// the only way to tell contention from a real race after the fact.
	page, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "the first run fails, as a contended one would") {
		t.Errorf("the job summary does not carry the tolerated failure's output:\n%s", page)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var measured map[string]float64
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	if measured["gateproof/cov"] != 100 {
		t.Fatalf("coverage = %v, want the re-measured 100, not the partial run's 50", measured)
	}
}
