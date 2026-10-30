// Package rollback_test: a rollback runbook runs each undo as the account its
// change ran as.
package rollback_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// TestRunbook_AStepRunsAsTheAccountItsNodeRanAs: a step whose change ran as
// another account is written with become, in a group and alone, and a step
// whose change ran as the login is written with none.
func TestRunbook_AStepRunsAsTheAccountItsNodeRanAs(t *testing.T) {
	levels := [][]rollback.Step{
		{{Node: "tasks[0]", Name: "undo a", FQCN: "noop", DeviceName: "r1", Become: "root"}},
		{
			{Node: "tasks[1]", Name: "undo b", FQCN: "noop", DeviceName: "r1", Become: "svc"},
			{Node: "tasks[1]", Name: "undo b", FQCN: "noop", DeviceName: "r2"},
		},
	}
	payload, _, err := rollback.Runbook("rb", levels, true)
	if err != nil {
		t.Fatal(err)
	}
	eval, _ := engine.NewCELEvaluator()
	dag, err := engine.NewBuilder(eval).BuildFromYAML(payload)
	if err != nil {
		t.Fatalf("the rollback runbook does not build: %v\n%s", err, payload)
	}
	got := map[string]string{}
	for _, task := range dag.Nodes {
		if task.FQCN != "" {
			user, _ := engine.BecomeFor(task)
			got[task.Name] = user
		}
	}
	want := map[string]string{"undo a on r1": "root", "undo b on r1": "svc", "undo b on r2": ""}
	for name, user := range want {
		if got[name] != user {
			t.Errorf("step %q becomes %q, want %q (all: %v)", name, got[name], user, got)
		}
	}
}
