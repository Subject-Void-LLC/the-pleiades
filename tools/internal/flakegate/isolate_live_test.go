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

	confirmed, contention, err := Isolate([]Failure{
		{Package: "gateproof", Test: "TestAlwaysPasses"},
		{Package: "gateproof", Test: "TestAlwaysFails"},
	}, nil, nil)
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
	confirmed, contention, err := Isolate([]Failure{
		{Package: "listedpkg", Test: "TestBrokenInsideAListedPackage"},
	}, nil, nil)
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
