// Tests that the CLI's rollback finds the task behind a journal entry of a
// loop item or a retry, whose node id has the item or try appended.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestAuthoredFrom_FindsTheTaskOfALoopItemAndATry: the lookup that reads a
// task's authored rollback: list takes the node id as the journal wrote it.
func TestAuthoredFrom_FindsTheTaskOfALoopItemAndATry(t *testing.T) {
	dir := t.TempDir()
	runbook := filepath.Join(dir, "r.yaml")
	body := "id: r\ntasks:\n  - exec.command: {argv: [\"true\"]}\n    loop: [1, 2]\n  - exec.command: {argv: [\"true\"]}\n    register: x\n    until: [\"true\"]\n    delay: 0\n"
	if err := os.WriteFile(runbook, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAMLFile(runbook)
	if err != nil {
		t.Fatal(err)
	}
	authored := authoredFrom(dir, runbook, []engine.JournalEntry{{DAGID: dag.ID, DAGVersion: dag.Version}})
	for _, node := range []string{"tasks[0]", "tasks[0].item[1]", "tasks[1].attempt[2]"} {
		if _, err := authored(node); err != nil {
			t.Errorf("node %s: %v", node, err)
		}
	}
	if _, err := authored("tasks[9].item[0]"); err == nil {
		t.Error("a node the runbook does not have was found")
	}
}
