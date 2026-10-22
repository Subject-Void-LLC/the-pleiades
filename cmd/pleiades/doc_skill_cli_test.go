// The skill's own runbook, checked through the real binary.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_DocCreateSkillTeachesARunbookThatValidates writes the skill
// through the real binary in a real project, takes the runbook it teaches
// by out of the file an agent would read, and validates it against a host
// tagged as the runbook targets, warnings counted as failures. A skill
// teaching a runbook that validation refuses would teach the agent the
// mistake (LESSONS 301: a documented example is a runbook).
func TestCLI_DocCreateSkillTeachesARunbookThatValidates(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "web01", "--type", "linux_server", "--tags", "web", "--set", "host=10.0.0.5"},
		{"doc", "--create-skill"},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	skill, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(skill), "```yaml\n")
	runbook, _, closed := strings.Cut(rest, "```")
	if !ok || !closed || !strings.Contains(runbook, "tasks:") {
		t.Fatalf("SKILL.md holds no runbook block:\n%s", skill)
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "kernel_report.yaml"), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runPleiades(t, dir, "validate", filepath.Join("runbooks", "kernel_report.yaml"))
	if err != nil || !strings.Contains(out, "validate: no issues found") || strings.Contains(out, "[warning]") {
		t.Errorf("the skill's runbook does not validate cleanly: err=%v\n%s\n--- runbook ---\n%s", err, out, runbook)
	}
}
