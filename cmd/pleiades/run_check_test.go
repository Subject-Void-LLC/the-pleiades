// Package main: in-process tests of `pleiades run --mode check`.
//
// check_exit_status_test.go drives the same statuses through the real
// binary, which is what proves the exit codes an operator and a pipeline
// see. These call runRunbook here, so the decisions behind those codes are
// covered as well: which tasks a check could not answer, which of those
// --allow-unchecked forgives, and the flags refused before anything runs.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkProject scaffolds a project holding two runbooks: one every task of
// which can be checked, and one carrying a task that cannot be, ssh_exec
// with no target. Neither needs a device.
func checkProject(t *testing.T) (dir string, complete, incomplete string) {
	t.Helper()
	dir = t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	const checkable = "  - name: fine\n    fqcn: noop\n"
	const uncheckable = "  - name: command\n    fqcn: ssh_exec\n    params:\n      cmd: uptime\n"
	write := func(name, tasks string) string {
		t.Helper()
		path := filepath.Join(dir, "runbooks", name)
		if err := os.WriteFile(path, []byte("id: "+strings.TrimSuffix(name, ".yaml")+"\ntasks:\n"+tasks), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return dir, write("complete.yaml", checkable), write("incomplete.yaml", checkable+uncheckable)
}

// statusOf is the exit status main would end with for err: whatever the
// error carries, 1 for any other failure, and 0 for none.
func statusOf(err error) int {
	if err == nil {
		return 0
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return 1
}

// TestRunRunbook_WhatACheckReportsAndEndsWith covers a check's own
// accounting: a runbook it can answer for completely ends at 0 saying
// nothing was changed, one it cannot names each task it could not check
// and ends at 3 without having failed anything, and --allow-unchecked
// still lists those tasks while accepting the gap it names.
func TestRunRunbook_WhatACheckReportsAndEndsWith(t *testing.T) {
	dir, complete, incomplete := checkProject(t)

	for _, tc := range []struct {
		name    string
		runbook string
		extra   []string
		want    int
		says    string
	}{
		{name: "every task checked", runbook: complete, want: 0, says: "check complete: nothing was changed"},
		{name: "each task's own answer", runbook: complete, extra: []string{"--verbose"}, want: 0, says: "predicted: true"},
		{name: "a task that cannot be checked", runbook: incomplete, want: 3, says: "check incomplete: 1 task(s)"},
		{name: "the gap allowed by name", runbook: incomplete, extra: []string{"--allow-unchecked", "ssh_exec"}, want: 0, says: "not checked, as --allow-unchecked allows"},
		{name: "another method allowed", runbook: incomplete, extra: []string{"--allow-unchecked", "exec.command"}, want: 3, says: "check incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{tc.runbook, "--dir", dir, "--mode", "check"}, tc.extra...)
			var out string
			err := captureRun(t, &out, func() error { return runRunbook(args) })
			if got := statusOf(err); got != tc.want {
				t.Errorf("%v ended at %d, want %d (%v):\n%s", tc.extra, got, tc.want, err, out)
			}
			// What an operator reads is the report plus the reason the
			// command ends with, which main prints.
			reported := out
			if err != nil {
				reported += err.Error()
			}
			if !strings.Contains(reported, tc.says) {
				t.Errorf("the report does not say %q:\n%s", tc.says, reported)
			}
		})
	}
}

// TestRunRunbook_ACheckWritesNoJournal pins the difference a check makes
// to the project on disk: the run journal is the record of what was
// changed, and a check changes nothing, so it writes none. The real run of
// the same runbook is the control, since a journal nothing ever writes
// would pass this test by doing nothing.
func TestRunRunbook_ACheckWritesNoJournal(t *testing.T) {
	dir, complete, _ := checkProject(t)
	var out string

	if err := captureRun(t, &out, func() error {
		return runRunbook([]string{complete, "--dir", dir, "--mode", "check"})
	}); err != nil {
		t.Fatalf("the check = %v:\n%s", err, out)
	}
	after := journalFiles(t, dir)
	if len(after) != 0 {
		t.Errorf("the check wrote %v", after)
	}

	if err := captureRun(t, &out, func() error {
		return runRunbook([]string{complete, "--dir", dir})
	}); err != nil {
		t.Fatalf("the real run = %v:\n%s", err, out)
	}
	if len(journalFiles(t, dir)) == 0 {
		t.Error("the real run wrote no journal either, so the check writing none proves nothing")
	}
}

// journalFiles lists every file under the project's journal directory.
func journalFiles(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(filepath.Join(dir, ".pleiades"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.Contains(path, "journal") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the project: %v", err)
	}
	return found
}

// TestRunRunbook_RefusesFlagsItCannotAct covers what `run` refuses before
// it loads or executes anything: a mode that is neither a run nor a check,
// which must not be read as a real run, and an --allow-unchecked with no
// method name, which would otherwise read as allowing everything.
func TestRunRunbook_RefusesFlagsItCannotAct(t *testing.T) {
	dir, complete, _ := checkProject(t)
	var out string

	err := captureRun(t, &out, func() error {
		return runRunbook([]string{complete, "--dir", dir, "--mode", "rehearse"})
	})
	if err == nil || !strings.Contains(err.Error(), "--mode") {
		t.Errorf("an unknown mode = %v, want it refused naming --mode", err)
	}

	err = captureRun(t, &out, func() error {
		return runRunbook([]string{complete, "--dir", dir, "--mode", "check", "--allow-unchecked", ""})
	})
	if err == nil || !strings.Contains(err.Error(), "needs a method name") {
		t.Errorf("an empty --allow-unchecked = %v, want it refused", err)
	}
}
