// CLI test through the built binary for run 10's second defect: --limit
// turned a target naming only hosts outside the limit into "matches no
// inventory host or tag", though validate passed the runbook. It contacts no
// device: it uses the engine's own actions.
package main_test

import (
	"strings"
	"testing"
)

// TestCLI_ATaskOutsideTheLimitIsSkipped: under --limit web1 the task naming
// the wan tag is skipped, naming edge1 and the limit, the hosts list reaches
// web1, and the run succeeds; a limit matching none of the hosts still fails.
func TestCLI_ATaskOutsideTheLimitIsSkipped(t *testing.T) {
	dir := varsProject(t, `# What this does: sets a fact on the wan router, then on every host.
# Blast radius: none (engine actions only, no device is contacted).
# Changes: nothing.
# Undo: nothing to undo.
id: narrowed
hosts: [web, wan]
tasks:
  - name: wan only
    set_fact:
      target: wan
      on_wan: true
  - name: every host
    set_fact:
      everywhere: true
`)
	out, err := runPleiades(t, dir, "run", "runbooks/check.yaml", "--limit", "web1")
	if err != nil || strings.Contains(out, "matches no inventory host or tag") ||
		!strings.Contains(out, `skipped (target "wan" names no device this run reaches: edge1 is outside --limit "web1")`) ||
		!strings.Contains(out, "tasks[1] [web1]: ok") {
		t.Fatalf("run --limit web1: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "run", "runbooks/check.yaml", "--limit", "nosuch"); err == nil || !strings.Contains(out, `--limit "nosuch" matches none of the runbook's hosts`) {
		t.Fatalf("run --limit nosuch: %v\n%s", err, out)
	}
}
