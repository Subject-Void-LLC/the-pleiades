// Package main_test: the status a check ends with (0, 3 or 1, and --allow-
// unchecked), through the real binary.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_CheckExitStatus covers the check's exit statuses through the
// real binary, with no device needed: noop can be checked and ssh_exec,
// with no target, cannot. 0 is a complete check, 3 an incomplete one
// that failed nothing, and 1 any failure, even beside an unchecked task.
// --allow-unchecked turns the named method's gaps into a complete check
// while still listing them, and a second, unnamed gap keeps it at 3.
func TestCLI_CheckExitStatus(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiadesWithEnv(t, dir, nil, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	write := func(name, tasks string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "runbooks", name), []byte("id: exit-"+strings.TrimSuffix(name, ".yaml")+"\ntasks:\n"+tasks), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const checkable = "  - name: fine\n    fqcn: noop\n"
	const uncheckable = "  - name: command\n    fqcn: ssh_exec\n    params:\n      cmd: uptime\n"
	const failing = "  - name: broken\n    fqcn: set_metadata\n"
	write("complete.yaml", checkable)
	write("incomplete.yaml", checkable+uncheckable)
	write("failed.yaml", uncheckable+failing)

	for _, tc := range []struct {
		runbook string
		extra   []string
		want    int
		says    string
	}{
		{runbook: "complete.yaml", want: 0, says: "check complete: nothing was changed"},
		{runbook: "complete.yaml", extra: []string{"--verbose"}, want: 0, says: "predicted: true"},
		{runbook: "incomplete.yaml", want: 3, says: "check incomplete: 1 task(s)"},
		{runbook: "failed.yaml", want: 1, says: "check failed"},
		{runbook: "incomplete.yaml", extra: []string{"--allow-unchecked", "ssh_exec"}, want: 0, says: "not checked, as --allow-unchecked allows"},
		{runbook: "incomplete.yaml", extra: []string{"--allow-unchecked", "exec.command"}, want: 3, says: "check incomplete"},
	} {
		args := append([]string{"run", "runbooks/" + tc.runbook, "--mode", "check"}, tc.extra...)
		out, err := runPleiadesWithEnv(t, dir, nil, args...)
		if got := exitCode(t, err); got != tc.want || !strings.Contains(out, tc.says) {
			t.Errorf("%v exited %d, want %d, and should say %q:\n%s", args, got, tc.want, tc.says, out)
		}
	}
}
