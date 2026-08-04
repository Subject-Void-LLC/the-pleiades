package main_test

import (
	"os/exec"
	"testing"
)

// BenchmarkPleiadesColdStart measures cold-start time for the pleiades
// binary: process launch, argument dispatch, and exit. Nothing else runs
// (no server, no database, no broker), so this is the CLI's actual floor.
func BenchmarkPleiadesColdStart(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if err := exec.Command(binPath, "--help").Run(); err != nil {
			// --help exits 0 by design; anything else is a real failure.
			b.Fatalf("pleiades --help failed: %v", err)
		}
	}
}

// BenchmarkAnsiblePlaybookColdStart is the honest comparison
// IMPLEMENTATION.md's Phase W1 asks for: ansible-playbook's own cold
// start, measured the same way. It is skipped, not faked, if
// ansible-playbook is not on PATH in this environment.
func BenchmarkAnsiblePlaybookColdStart(b *testing.B) {
	binary, err := exec.LookPath("ansible-playbook")
	if err != nil {
		b.Skip("ansible-playbook not found on PATH; skipping the comparison rather than fabricating a number")
	}

	for i := 0; i < b.N; i++ {
		if err := exec.Command(binary, "--version").Run(); err != nil {
			b.Fatalf("ansible-playbook --version failed: %v", err)
		}
	}
}
