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

// TestCLI_ARescueTheRunCannotReachFailsTheRun: a block that fails on web1
// with a rescue naming only the wan tag is not rescued under --limit web1 nor
// in a serial: batch without edge1: the run exits non-zero, naming the limit
// or the batch, and never says it completed through a rescue.
func TestCLI_ARescueTheRunCannotReachFailsTheRun(t *testing.T) {
	const runbook = `# What this does: fails on purpose, with a rescue and an always on the wan router.
# Blast radius: none (engine actions only, no device is contacted).
# Changes: nothing.
# Undo: nothing to undo.
id: rescue_on_wan
hosts: [web, wan]
SERIAL
tasks:
  - name: guarded
    block:
      - name: break it
        fail:
          target: web1
          msg: on purpose
    rescue:
      - name: fix on wan
        set_fact:
          target: wan
          fixed: true
    always:
      - name: cleanup on wan
        set_fact:
          target: wan
          cleaned: true
`
	for serial, why := range map[string]string{"": `edge1 is outside --limit "web1"`, "serial: 1\n": "edge1 is in another serial: batch"} {
		dir := varsProject(t, strings.Replace(runbook, "SERIAL\n", serial, 1))
		args := []string{"run", "runbooks/check.yaml"}
		if serial == "" {
			args = append(args, "--limit", "web1")
		}
		out, err := runPleiades(t, dir, args...)
		if err == nil || strings.Contains(out, "run complete through a rescue") || strings.Contains(out, "RESCUED") ||
			!strings.Contains(out, "does not answer the failure its block is handling") || !strings.Contains(out, why) {
			t.Errorf("%q: %v\n%s", serial, err, out)
		}
	}
}
