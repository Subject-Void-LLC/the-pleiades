// Tests for `pleiades validate` given several runbooks, or none, through
// the real binary.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProjectFile writes content to name under dir, making any directory
// it needs, and fails the test on any I/O error.
func writeProjectFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// assertLines fails the test unless out contains every line in want, each
// as a whole line.
func assertLines(t *testing.T, out string, want ...string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	for _, w := range want {
		found := false
		for _, line := range lines {
			if line == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("output has no line %q:\n%s", w, out)
		}
	}
}

// TestCLI_ValidateManyRunbooks drives validate the ways a user reaches
// more than one runbook: bare, which checks the project's runbooks
// directory, and a list of paths, which is what a shell glob such as
// runbooks/* hands it. It covers what each kind of entry becomes (a
// runbook checked, a directory, a non-YAML file and an import_tasks file
// passed over), that one bad runbook fails the command without hiding the
// others, and that --tags and --dir mean the same thing they do for one
// runbook.
func TestCLI_ValidateManyRunbooks(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "webserver1", "--type", "linux_server"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	writeProjectFile(t, dir, "runbooks/tasks/common.yaml", "- name: shared\n  fqcn: noop\n")
	writeProjectFile(t, dir, "runbooks/main.yaml", "id: main\ntasks:\n  - name: setup\n    fqcn: import_tasks\n    params:\n      file: tasks/common.yaml\n")
	writeProjectFile(t, dir, "runbooks/web.yaml", "id: web\ntasks:\n  - name: deploy\n    fqcn: noop\n    tags: web\n")
	writeProjectFile(t, dir, "runbooks/needs_ios.yaml", "id: needs-ios\ntasks:\n  - name: backup\n    fqcn: ios_backup\n    params:\n      target: webserver1\n")
	writeProjectFile(t, dir, "runbooks/README.md", "# What these runbooks do\n")
	writeProjectFile(t, dir, "runbooks/.draft.yaml", "not: [a runbook\n")

	// Bare: every entry of runbooks/ but the hidden one, in name order,
	// and the capability finding fails the run without stopping it.
	out, err := runPleiades(t, dir, "validate")
	if err == nil {
		t.Fatalf("validate passed a project with a runbook that needs a missing capability:\n%s", out)
	}
	assertLines(t, out,
		"runbooks/README.md: skipped, not a .yaml or .yml file",
		"runbooks/main.yaml: no issues found",
		"runbooks/needs_ios.yaml:",
		"runbooks/sample.yaml: no issues found",
		"runbooks/tasks: skipped, a directory",
		"runbooks/web.yaml: no issues found",
		"validate: 4 runbooks checked, 2 skipped, 1 with problems",
		"pleiades: validation failed: 1 of 4 runbooks",
	)
	if !strings.Contains(out, "\n  [") || !strings.Contains(out, "CiscoIOSCapable") {
		t.Errorf("the finding is not listed under its runbook:\n%s", out)
	}
	if strings.Contains(out, ".draft.yaml") {
		t.Errorf("a hidden file was read, which runbooks/* would not name:\n%s", out)
	}

	// Named: an import_tasks file is passed over, and the rest pass.
	out, err = runPleiades(t, dir, "validate", "runbooks/main.yaml", "runbooks/sample.yaml", "runbooks/tasks/common.yaml", "runbooks/web.yaml")
	if err != nil {
		t.Fatalf("validate of good runbooks failed: %v\n%s", err, out)
	}
	assertLines(t, out,
		"runbooks/tasks/common.yaml: skipped, a list of tasks, checked through the runbook that imports it",
		"validate: 3 runbooks checked, 1 skipped, no issues found",
	)

	// A tag one runbook carries is not a typo for the others, and flags
	// may sit between the runbooks.
	if out, err := runPleiades(t, dir, "validate", "runbooks/sample.yaml", "--tags", "web", "runbooks/web.yaml"); err != nil {
		t.Errorf("validate --tags web over two runbooks: %v\n%s", err, out)
	}
	out, err = runPleiades(t, dir, "validate", "runbooks/sample.yaml", "runbooks/web.yaml", "--tags", "wbe")
	if err == nil || !strings.Contains(out, `--tags names "wbe", which no task carries; the tags these runbooks carry are web`) {
		t.Errorf("validate accepted a tag neither runbook carries: %v\n%s", err, out)
	}

	// --dir names the project a bare validate checks, from anywhere.
	if err := os.Remove(filepath.Join(dir, "runbooks", "needs_ios.yaml")); err != nil {
		t.Fatal(err)
	}
	out, err = runPleiades(t, t.TempDir(), "validate", "--dir", dir)
	if err != nil {
		t.Fatalf("validate --dir: %v\n%s", err, out)
	}
	assertLines(t, out,
		filepath.Join(dir, "runbooks", "web.yaml")+": no issues found",
		"validate: 3 runbooks checked, 2 skipped, no issues found",
	)
}

// TestCLI_ValidateNothingToCheck covers the ways validate ends with no
// runbook checked, each of which fails rather than reporting success.
func TestCLI_ValidateNothingToCheck(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	writeProjectFile(t, dir, "runbooks/tasks/common.yaml", "- name: shared\n  fqcn: noop\n")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"an import_tasks file alone", []string{"validate", "runbooks/tasks/common.yaml"}, "pleiades: no runbook to validate"},
		{"a directory alone", []string{"validate", "runbooks/tasks"}, "pleiades: no runbook to validate"},
		{"only skipped files", []string{"validate", "runbooks/tasks", "runbooks/tasks/common.yaml"}, "validate: 0 runbooks checked, 2 skipped"},
		{"a missing runbook", []string{"validate", "runbooks/nope.yaml"}, "pleiades: failed to build DAG from runbooks/nope.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runPleiades(t, dir, tc.args...)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Errorf("%v = %v, want a failure containing %q:\n%s", tc.args, err, tc.want, out)
			}
		})
	}

	// A project with no runbooks directory says so, and how to name one.
	empty := t.TempDir()
	writeProjectFile(t, empty, "inventory.yaml", "hosts: []\n")
	out, err := runPleiades(t, empty, "validate")
	if err == nil || !strings.Contains(out, "no runbook named") || !strings.Contains(out, "pleiades validate <runbook.yaml>") {
		t.Errorf("validate in a project without runbooks = %v:\n%s", err, out)
	}
}
