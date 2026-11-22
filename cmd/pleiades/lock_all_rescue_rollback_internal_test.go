// Test of the recovery property behind FAILURE_PATTERNS 1355's fix: a
// runbook validation refuses must still compile, because a rollback finds
// the runbook its run ran by compiling it.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestRollbackFindsARunbookWhoseRescueTakesEveryLock: findRunbook, which
// `pleiades rollback` plans from, finds a runbook with lock_acquisition:
// all_at_plan_time on a rescue task by the id and version a journal would
// name, from the runbooks directory and through --runbook. validate and run
// refuse such a runbook today, and a run from before they did may have
// changed devices before it reached the rescue. The builder must not refuse
// it: a runbook that does not compile is skipped here as "not the one that
// ran", and the key cannot be edited away, since it is part of the version.
func TestRollbackFindsARunbookWhoseRescueTakesEveryLock(t *testing.T) {
	const runbook = `# What this does: fails on purpose, with a rescue that sets a fact.
# Blast radius: none (engine actions only, no device is contacted).
# Changes: nothing.
# Undo: nothing to undo.
id: rescue_lock_all
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
`
	dir := t.TempDir()
	path := filepath.Join(dir, "runbooks", "check.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	ran, err := engine.NewBuilder(eval).BuildFromYAMLFile(path)
	if err != nil {
		t.Fatalf("the runbook does not compile, so a run of it could never be rolled back: %v", err)
	}
	for _, explicit := range []string{"", path} {
		found, err := findRunbook(dir, explicit, ran.ID, ran.Version)
		if err != nil || found == nil || found.Version != ran.Version {
			t.Errorf("findRunbook(--runbook %q): %v", explicit, err)
		}
	}
	// Taking the key out makes it another runbook, which a rollback refuses.
	without, err := engine.NewBuilder(eval).BuildFromYAML([]byte(runbook[:len(runbook)-len("        lock_acquisition: all_at_plan_time\n")]))
	if err != nil {
		t.Fatal(err)
	}
	if without.Version == ran.Version {
		t.Error("the runbook's version does not cover lock_acquisition, so this test's premise is false")
	}
}
