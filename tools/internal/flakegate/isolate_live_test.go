// Package flakegate: the isolation pass, proved against real `go test`
// runs rather than a fake.
//
// These build throwaway modules and shell out to the real toolchain,
// because the claim under test is exactly that a second, narrower `go test`
// invocation answers a question the first one could not. A fake that
// returned a canned pass or fail would only prove the canned answer was
// canned, which is the RULE 0 objection this repository applies everywhere
// else.
package flakegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsolate_DistinguishesAContendedTestFromABrokenOne is the rule this
// pass exists for, proved against real `go test` runs rather than a fake.
//
// It builds a throwaway module holding two tests: one that always passes
// and one that always fails. Both are handed to Isolate as though the big
// parallel run had reported them failing. The passing one must come back as
// contention, and the failing one must come back confirmed -- which is the
// whole difference between "your machine was busy" and "you broke it", and
// the difference a static package list cannot see.
func TestIsolate_DistinguishesAContendedTestFromABrokenOne(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}

	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("go.mod", "module gateproof\n\ngo 1.22\n")
	write("proof_test.go", `package gateproof

import "testing"

func TestAlwaysPasses(t *testing.T) {}

func TestAlwaysFails(t *testing.T) { t.Fatal("deliberately broken") }
`)

	// Run from inside the throwaway module, since Isolate shells out to
	// `go test <package>` in the working directory.
	restore, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })

	confirmed, contention, notRun, err := Isolate([]Failure{
		{Package: "gateproof", Test: "TestAlwaysPasses"},
		{Package: "gateproof", Test: "TestAlwaysFails"},
	}, nil, nil)
	if len(notRun) != 0 {
		t.Fatalf("two failures were deferred rather than re-run: %+v", notRun)
	}
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}

	if len(contention) != 1 || contention[0].Test != "TestAlwaysPasses" {
		t.Errorf("contention = %+v, want only the test that passes alone", contention)
	}
	if len(confirmed) != 1 || confirmed[0].Test != "TestAlwaysFails" {
		t.Errorf("confirmed = %+v, want only the test that fails alone", confirmed)
	}
}

// TestIsolate_AListedPackageGetsNoProtectionFromARealFailure is the
// regression this whole pass is aimed at.
//
// Four e2e tests once failed identically in five consecutive push-gate
// runs, were warned about every time because their package was listed, and
// the defect behind them was a total outage in which every job hung
// forever. A waiver is a statement about why a package loses races, and it
// has never been a statement that the package's tests may fail.
func TestIsolate_AListedPackageGetsNoProtectionFromARealFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module listedpkg\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "listed_test.go"), []byte(`package listedpkg

import "testing"

func TestBrokenInsideAListedPackage(t *testing.T) { t.Fatal("a real defect") }
`), 0o600); err != nil {
		t.Fatalf("writing the test: %v", err)
	}

	restore, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })

	// Isolate is not told about the waiver list at all, and that is the
	// design: the list decides what a failure is CALLED, never whether it
	// counts. testgate hands it every failure, listed or not.
	confirmed, contention, notRun, err := Isolate([]Failure{
		{Package: "listedpkg", Test: "TestBrokenInsideAListedPackage"},
	}, nil, nil)
	if len(notRun) != 0 {
		t.Fatalf("one failure was deferred rather than re-run: %+v", notRun)
	}
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}
	if len(contention) != 0 {
		t.Fatalf("a test that fails alone was tolerated: %+v", contention)
	}
	if len(confirmed) != 1 {
		t.Fatalf("confirmed = %+v, want the real failure", confirmed)
	}
	if !strings.Contains(confirmed[0].Test, "Broken") {
		t.Errorf("confirmed the wrong test: %+v", confirmed)
	}
}

// TestIsolate_ATimedOutPackageIsReRunNotSkipped is the shape the pass first
// declined to examine, and the commonest contention symptom there is.
//
// go test reports a package that exceeded its -timeout as a bare
// package-level fail with no test to blame. That was classified as a build
// failure, so Isolate confirmed it without asking twice and testgate
// printed "build failed" -- about a package that compiles perfectly well.
func TestIsolate_ATimedOutPackageIsReRunNotSkipped(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}

	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("go.mod", "module timeoutproof\n\ngo 1.22\n")
	write("proof_test.go", `package timeoutproof

import "testing"

func TestFine(t *testing.T) {}
`)

	restore, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })

	// A package-level failure, exactly as a timeout arrives: no test named.
	confirmed, contention, notRun, err := Isolate(
		[]Failure{{Package: "timeoutproof", Kind: FailedPackage}}, nil, nil)
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}
	if len(notRun) != 0 {
		t.Fatalf("the package was deferred rather than re-run: %+v", notRun)
	}
	// The package passes when it is not competing for a machine, so the
	// re-run says contention. Under the old classification it was never
	// asked at all.
	if len(contention) != 1 || contention[0].Package != "timeoutproof" {
		t.Errorf("contention = %+v, confirmed = %+v; want the package re-run and found healthy",
			contention, confirmed)
	}
}

// TestIsolate_AReRunThatCannotBuildIsInconclusiveNotAFailure covers the
// verdict this pass used to infer from an exit code.
//
// go test exits non-zero for a test failing, a package not compiling, a
// pattern matching nothing, and the toolchain falling over. Only the first
// is evidence about the test. Reading the exit status alone reported a
// broken tree as "this test failed again when re-run alone", which sends
// somebody looking for a defect the run never examined.
func TestIsolate_AReRunThatCannotBuildIsInconclusiveNotAFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module brokenproof\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	// Does not compile.
	if err := os.WriteFile(filepath.Join(dir, "broken_test.go"), []byte(`package brokenproof

import "testing"

func TestBroken(t *testing.T) { this is not go }
`), 0o600); err != nil {
		t.Fatalf("writing the test: %v", err)
	}

	restore, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })

	confirmed, contention, _, err := Isolate(
		[]Failure{{Package: "brokenproof", Test: "TestBroken"}}, nil, nil)
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}
	// Confirmed, because a check that could not be performed must never
	// tolerate a failure -- but reached through the inconclusive path
	// rather than by reading an exit code as "the test failed".
	if len(contention) != 0 {
		t.Errorf("a re-run that could not build was tolerated: %+v", contention)
	}
	if len(confirmed) != 1 {
		t.Errorf("confirmed = %+v, want the unanswerable re-run", confirmed)
	}
}

// TestIsolate_APatternMatchingNothingIsNotAPass is the fail-open direction
// this pass has to refuse.
//
// If a re-run's -run pattern matches no test -- a renamed test, a subtest
// spelling the parent lookup got wrong -- go test exits ZERO having run
// nothing. Read as a pass, that tolerates a failure nobody re-examined.
func TestIsolate_APatternMatchingNothingIsNotAPass(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module emptyproof\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty_test.go"), []byte(`package emptyproof

import "testing"

func TestSomethingElse(t *testing.T) {}
`), 0o600); err != nil {
		t.Fatalf("writing the test: %v", err)
	}

	restore, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })

	confirmed, contention, _, err := Isolate(
		[]Failure{{Package: "emptyproof", Test: "TestGoneAway"}}, nil, nil)
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}
	if len(contention) != 0 {
		t.Fatalf("a re-run that ran nothing was read as a pass: %+v", contention)
	}
	if len(confirmed) != 1 {
		t.Errorf("confirmed = %+v, want the unanswerable re-run", confirmed)
	}
}
