// Package validate_test: an empty rollback: list, rollback: [], is an
// authored answer to the reversible and rollback_on_failure rules, as a list
// with steps is: its author says undoing the task means doing nothing.
// Before, both rules read it as no list and refused the task.
package validate_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

func TestReversibleRule_AnEmptyRollbackListAnswers(t *testing.T) {
	registerUndoShapes(t)
	for _, method := range []string{"undo.never", "ssh_exec"} {
		runbook := reversibleRunbook(method, "    rollback: []\n")
		if findings := validate.ReversibleRule(validate.WorldView{DAG: yamlDAG(t, runbook)}); len(findings) != 0 {
			t.Errorf("%s with rollback: [] was refused: %+v", method, findings)
		}
	}
}

func TestRollbackOnFailureRule_AnEmptyRollbackListAnswers(t *testing.T) {
	registerUndoShapes(t)
	runbook := `id: r
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: never, but nothing to undo
        undo.never: {name: a}
        rollback: []
      - name: never
        undo.never: {name: a}
`
	findings := validate.RollbackOnFailureRule(validate.WorldView{DAG: yamlDAG(t, runbook)})
	if len(findings) != 1 || findings[0].Node != "tasks[0].block[1]" {
		t.Errorf("findings %+v, want only the task with no list refused", findings)
	}
}
