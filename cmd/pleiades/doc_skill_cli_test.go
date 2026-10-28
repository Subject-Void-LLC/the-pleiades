// The skill's own runbooks, checked through the real binary, and the
// first run against a real sshd.
package main_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// TestCLI_DocCreateSkillTeachesARunbookThatValidatesAndRuns writes the
// skill through the real binary in a real project, takes the runbook it
// teaches by out of the file an agent would read, validates it against a
// host tagged as the runbook targets (warnings counted as failures), and
// then runs it against a real sshd. Validation does not evaluate a when:
// condition, so only the run shows that the second task's condition reads
// the first task's register as written: both tasks must run. A skill
// teaching a runbook that validation refuses, or one whose condition never
// holds, would teach the agent the mistake (LESSONS 301; FAILURE_PATTERNS
// 324, 548).
func TestCLI_DocCreateSkillTeachesARunbookThatValidatesAndRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the SSH container test in short mode")
	}
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "web01", "--type", "linux_server", "--tags", "web", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "web01", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
		{"doc", "--create-skill"},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("pleiades %s: %v\n%s", args[0], err, out)
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

	out, err := runPleiadesWithHome(t, dir, home, "validate", filepath.Join("runbooks", "kernel_report.yaml"))
	if err != nil || !strings.Contains(out, "validate: no issues found") || strings.Contains(out, "[warning]") {
		t.Fatalf("the skill's runbook does not validate cleanly: err=%v\n%s\n--- runbook ---\n%s", err, out, runbook)
	}

	out, err = runPleiadesWithHome(t, dir, home, "run", filepath.Join("runbooks", "kernel_report.yaml"), "--json")
	if err != nil {
		t.Fatalf("the skill's runbook does not run: %v\n%s", err, out)
	}
	var report struct {
		Tasks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"tasks"`
		Outcome struct {
			Status string `json:"status"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("run --json printed no report: %v\n%s", err, out)
	}
	if report.Outcome.Status != "complete" || len(report.Tasks) != 2 {
		t.Fatalf("run ended %q with %d task(s), want complete with both:\n%s", report.Outcome.Status, len(report.Tasks), out)
	}
	for _, task := range report.Tasks {
		// exec.command reports changed whenever it runs; skipped would mean
		// the condition did not hold.
		if task.Status != "changed" {
			t.Errorf("task %q ended %q, want it to have run:\n%s", task.Name, task.Status, out)
		}
	}
}

// yamlBlocks returns every fenced yaml block of a Markdown text, in order.
func yamlBlocks(markdown string) []string {
	var blocks []string
	rest := markdown
	for {
		var ok bool
		if _, rest, ok = strings.Cut(rest, "```yaml\n"); !ok {
			return blocks
		}
		block, after, closed := strings.Cut(rest, "```")
		if !closed {
			return blocks
		}
		blocks, rest = append(blocks, block), after
	}
}

// TestCLI_DocCreateSkillsRollbackExampleValidates writes the skill through
// the real binary, takes the runbook its network section teaches a block's
// rollback_on_failure by (net.ios.config with no rollback: list) out of the
// file an agent reads, and validates it against a Cisco router as named,
// warnings counted as failures. The skill once said this works while
// validate refused every such block; a skill that teaches what validate
// refuses teaches the agent the mistake (FAILURE_PATTERNS 548, 702).
func TestCLI_DocCreateSkillsRollbackExampleValidates(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "core1", "--type", "cisco_router", "--set", "host=192.0.2.10"},
		{"doc", "--create-skill"},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("pleiades %s: %v\n%s", args[0], err, out)
		}
	}
	skill, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	var runbook string
	for _, block := range yamlBlocks(string(skill)) {
		if strings.Contains(block, "rollback_on_failure: true") {
			runbook = block
		}
	}
	if !strings.Contains(runbook, "net.ios.config:") || strings.Contains(runbook, "rollback:\n") {
		t.Fatalf("SKILL.md holds no rollback_on_failure example over net.ios.config without a rollback: list:\n%s", skill)
	}
	path := filepath.Join("runbooks", "loopback_change.yaml")
	if err := os.WriteFile(filepath.Join(dir, path), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runPleiadesWithHome(t, dir, home, "validate", path)
	if err != nil || !strings.Contains(out, "validate: no issues found") || strings.Contains(out, "[warning]") {
		t.Fatalf("the skill's rollback example does not validate cleanly: err=%v\n%s\n--- runbook ---\n%s", err, out, runbook)
	}
}
