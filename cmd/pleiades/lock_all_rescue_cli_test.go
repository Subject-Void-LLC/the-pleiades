// CLI test through the built binary for FAILURE_PATTERNS 1355: a rescue task
// taking lock_acquisition: all_at_plan_time passed validate and was refused
// only when a run reached the rescue, after a task had failed on a device.
// It contacts no device: it uses the engine's own actions.
package main_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCLI_ARescueTaskTakingEveryLockIsRefusedBeforeTheRun: validate refuses
// the runbook, and run refuses it by the same validation before its first
// task, in a real run and in a check alike, in the same words. In a block
// that runs as one unit (here, one whose task names a target other than the
// runbook's hosts) the rescue may take the lock, so validate passes it and
// the run is rescued; under a tag selection that leaves the block handling
// failures per device, validate and run refuse it again, before any task
// runs.
func TestCLI_ARescueTaskTakingEveryLockIsRefusedBeforeTheRun(t *testing.T) {
	const header = `# What this does: fails on purpose, with a rescue that sets a fact.
# Blast radius: none (engine actions only, no device is contacted).
# Changes: nothing.
# Undo: nothing to undo.
`
	dir := varsProject(t, header+"id: placeholder\nhosts: web\ntasks:\n  - name: nothing\n    set_fact:\n      nothing: true\n")

	writeProject(t, dir, map[string]string{"runbooks/check.yaml": header + `id: rescue_lock_all
hosts: web
tasks:
  - name: guarded
    block:
      - name: break it
        fail:
          msg: on purpose
    rescue:
      - name: fix it
        set_fact:
          fixed: true
        lock_acquisition: all_at_plan_time
`})
	for _, args := range [][]string{{"validate"}, {"run"}, {"run", "--mode", "check"}} {
		out, err := runPleiades(t, dir, append(args, "runbooks/check.yaml")...)
		if err == nil || !strings.Contains(out, `(name "fix it"): `+engine.ErrRescueLockAll) || strings.Contains(out, "on purpose") || strings.Contains(out, "executing:") {
			t.Errorf("%v: %v\n%s", args, err, out)
		}
		if args[0] == "run" && !strings.Contains(out, "validation failed") {
			t.Errorf("%v was not refused by validation:\n%s", args, out)
		}
	}

	// One task of the block names a target other than the runbook's hosts, so
	// the block runs as one unit and its rescue is decided once: it may take
	// every lock.
	writeProject(t, dir, map[string]string{"runbooks/check.yaml": header + `id: rescue_lock_all_unit
hosts: web
tasks:
  - name: guarded
    block:
      - name: break it
        fail:
          msg: on purpose
      - name: elsewhere
        tags: [other]
        set_fact:
          target: wan
          seen: true
    rescue:
      - name: fix it
        set_fact:
          fixed: true
        lock_acquisition: all_at_plan_time
`})
	if out, err := runPleiades(t, dir, "validate", "runbooks/check.yaml"); err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate, the block a unit: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "run", "runbooks/check.yaml"); err != nil || !strings.Contains(out, "through a rescue") {
		t.Errorf("run, the block a unit: %v\n%s", err, out)
	}
	// Without that task the block handles failures per device, and the same
	// rescue cannot take the lock: both commands say so before anything runs.
	for _, command := range []string{"validate", "run"} {
		out, err := runPleiades(t, dir, command, "runbooks/check.yaml", "--skip-tags", "other")
		if err == nil || !strings.Contains(out, `(name "fix it"): `+engine.ErrRescueLockAll) || strings.Contains(out, "on purpose") || strings.Contains(out, "executing:") {
			t.Errorf("%s --skip-tags other: %v\n%s", command, err, out)
		}
	}
}
