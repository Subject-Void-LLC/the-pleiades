// Tests for `pleiades forge migrate-playbook` through the real binary:
// what it writes and where, what it refuses to overwrite, its exit codes,
// and its JSON report.
package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanPlaybook converts with nothing for a person to do.
const cleanPlaybook = `- hosts: web
  gather_facts: false
  tasks:
    - name: make the directory
      ansible.builtin.file: {path: /srv/app, state: directory, mode: "0755"}
`

// blockedPlaybook holds a task that cannot convert.
const blockedPlaybook = `- hosts: web
  gather_facts: false
  tasks:
    - name: render the config
      ansible.builtin.template: {src: app.j2, dest: /etc/app.conf}
`

// runMigrate runs migrate-playbook in dir and returns stdout, stderr and
// the exit code, kept apart so --json output can be parsed as it is.
func runMigrate(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binPath, append([]string{"forge", "migrate-playbook"}, args...)...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = exitCode(t, err)
	}
	return stdout.String(), stderr.String(), code
}

// writeFile writes content under dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCLI_MigratePlaybook_Writes converts a clean playbook: exit 0, the
// runbook written under --out's default, and the report on stdout.
func TestCLI_MigratePlaybook_Writes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site.yml", cleanPlaybook)
	stdout, stderr, code := runMigrate(t, dir, "site.yml")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	written, err := os.ReadFile(filepath.Join(dir, "runbooks", "site.yaml"))
	if err != nil {
		t.Fatalf("no runbook written: %v\n%s", err, stdout)
	}
	if !strings.Contains(string(written), "fqcn: file.directory") {
		t.Errorf("runbook does not hold the converted task:\n%s", written)
	}
	if !strings.Contains(stdout, "1 converted") {
		t.Errorf("report lacks its counts:\n%s", stdout)
	}
}

// TestCLI_MigratePlaybook_IncompleteExits3 converts a playbook with a
// blocked task: the runbook is written under its incomplete name and the
// exit code says a person must act.
func TestCLI_MigratePlaybook_IncompleteExits3(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site.yml", blockedPlaybook)
	stdout, stderr, code := runMigrate(t, dir, "site.yml")
	if code != 3 {
		t.Fatalf("exit %d, want 3\n%s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "runbooks", "site.incomplete.yaml")); err != nil {
		t.Errorf("incomplete runbook not written under its incomplete name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runbooks", "site.yaml")); err == nil {
		t.Error("an incomplete runbook was written under the complete name")
	}
}

// TestCLI_MigratePlaybook_RefusesOverwrite runs twice: the second run
// writes nothing and names the file; --force replaces it; and converting
// to the other state names the stale file left from before.
func TestCLI_MigratePlaybook_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site.yml", cleanPlaybook)
	if _, stderr, code := runMigrate(t, dir, "site.yml"); code != 0 {
		t.Fatalf("first run exit %d: %s", code, stderr)
	}
	target := filepath.Join(dir, "runbooks", "site.yaml")
	writeFile(t, filepath.Join(dir, "runbooks"), "site.yaml", "edited by hand\n")
	_, stderr, code := runMigrate(t, dir, "site.yml")
	if code == 0 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("second run exit %d, want a refusal naming the file: %s", code, stderr)
	}
	if got, _ := os.ReadFile(target); string(got) != "edited by hand\n" {
		t.Errorf("the refused run changed the file: %q", got)
	}
	if _, stderr, code := runMigrate(t, dir, "site.yml", "--force"); code != 0 {
		t.Fatalf("--force exit %d: %s", code, stderr)
	}
	if got, _ := os.ReadFile(target); !strings.Contains(string(got), "file.directory") {
		t.Errorf("--force did not replace the file: %q", got)
	}
	writeFile(t, dir, "site.yml", blockedPlaybook)
	if _, stderr, _ := runMigrate(t, dir, "site.yml"); !strings.Contains(stderr, "site.yaml is left from an earlier conversion") {
		t.Errorf("the stale complete runbook was not named: %s", stderr)
	}
}

// TestCLI_MigratePlaybook_RefusesSymlinks refuses an --out that is a
// symbolic link, and a symbolic link where a runbook goes, even with
// --force, so the output cannot be redirected.
func TestCLI_MigratePlaybook_RefusesSymlinks(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	writeFile(t, dir, "site.yml", cleanPlaybook)
	if err := os.Symlink(elsewhere, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runMigrate(t, dir, "site.yml", "--out", "linked"); code == 0 || !strings.Contains(stderr, "symbolic link") {
		t.Errorf("a symlinked --out was accepted (exit %d): %s", code, stderr)
	}
	if err := os.Mkdir(filepath.Join(dir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "victim"), filepath.Join(dir, "out", "site.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runMigrate(t, dir, "site.yml", "--out", "out", "--force"); code == 0 || !strings.Contains(stderr, "symbolic link") {
		t.Errorf("a symlink at the runbook's name was written through (exit %d): %s", code, stderr)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("something was written outside --out: %v", entries)
	}
}

// TestCLI_MigratePlaybook_JSON prints the report as JSON on stdout alone,
// with the schema version and the same counts the text view prints, and
// escapes playbook text the text view would send to a terminal.
func TestCLI_MigratePlaybook_JSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site.yml", strings.Replace(blockedPlaybook, "name: render the config", `name: "render\e[2J the config"`, 1))
	stdout, stderr, code := runMigrate(t, dir, "site.yml", "--json")
	if code != 3 {
		t.Fatalf("exit %d, want 3: %s", code, stderr)
	}
	var report struct {
		SchemaVersion int `json:"schema_version"`
		Counts        struct {
			Blocked int `json:"blocked"`
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not the JSON report: %v\n%s", err, stdout)
	}
	if report.SchemaVersion == 0 || report.Counts.Blocked != 1 {
		t.Errorf("report = %+v", report)
	}
	text, _, _ := runMigrate(t, dir, "site.yml", "--force")
	if strings.ContainsRune(text, 0x1b) {
		t.Error("the text report sent a raw escape character to the terminal")
	}
}

// TestCLI_MigratePlaybook_BadInvocations covers what is refused before
// anything is converted.
func TestCLI_MigratePlaybook_BadInvocations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site.yml", cleanPlaybook)
	writeFile(t, dir, "plain", "x")
	for _, args := range [][]string{
		{},
		{"missing.yml"},
		{"site.yml", "extra"},
		{"site.yml", "--out", "plain"},
		{"site.yml", "--bogus"},
	} {
		if stdout, stderr, code := runMigrate(t, dir, args...); code == 0 {
			t.Errorf("%v succeeded: %s%s", args, stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "runbooks")); err == nil {
		t.Error("a refused invocation created --out")
	}
}
