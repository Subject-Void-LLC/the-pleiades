package engine_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// TestDAGBuilder_LinearTasks confirms a simple tasks: list compiles with
// the expected synthesized IDs and a happy-path Adjacency chain that
// connects them in order.
func TestDAGBuilder_LinearTasks(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-1",
		"tasks": [
			{"name": "start", "fqcn": "noop"},
			{"name": "ping", "fqcn": "ssh_exec"},
			{"name": "reboot", "fqcn": "ios_backup", "when": "stat.ping_ms > 100"}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}
	if len(dag.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(dag.Nodes))
	}

	for _, id := range []string{"tasks[0]", "tasks[1]", "tasks[2]"} {
		if _, ok := dag.Nodes[id]; !ok {
			t.Errorf("expected node %q to exist, got nodes: %v", id, dag.Nodes)
		}
	}

	// The happy-path chain runs tasks[0] -> tasks[1] -> tasks[2].
	if len(dag.Adjacency["tasks[0]"]) != 1 || dag.Adjacency["tasks[0]"][0].To != "tasks[1]" {
		t.Fatalf("expected tasks[0] -> tasks[1], got %v", dag.Adjacency["tasks[0]"])
	}
	if len(dag.Adjacency["tasks[1]"]) != 1 || dag.Adjacency["tasks[1]"][0].To != "tasks[2]" {
		t.Fatalf("expected tasks[1] -> tasks[2], got %v", dag.Adjacency["tasks[1]"])
	}

	// verify CEL compilation occurred for the last task's when condition
	if dag.Conditions["tasks[2]"] == nil {
		t.Errorf("expected condition to be compiled for tasks[2]")
	}
	if dag.Conditions["tasks[0]"] != nil {
		t.Errorf("expected no condition for tasks[0], got %v", dag.Conditions["tasks[0]"])
	}
}

// TestDAGBuilder_WhenCondition confirms a task with a "when" condition
// gets a non-nil entry in DAG.Conditions, keyed by its synthesized ID.
func TestDAGBuilder_WhenCondition(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-when",
		"tasks": [
			{"name": "conditional step", "fqcn": "noop", "when": "stat.ok == true"}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}
	if dag.Conditions["tasks[0]"] == nil {
		t.Errorf("expected a compiled condition for tasks[0], got nil")
	}
}

// TestDAGBuilder_BlockRescueAlways confirms a block task's Block children
// splice into the happy-path chain, while its Rescue and Always children
// land in DAG.Nodes (for coverage) but never appear in the happy-path
// Adjacency chain.
func TestDAGBuilder_BlockRescueAlways(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-block",
		"tasks": [
			{"name": "before", "fqcn": "noop"},
			{"name": "risky", "block": [
				{"name": "b0", "fqcn": "ssh_exec"},
				{"name": "b1", "fqcn": "ssh_exec"}
			], "rescue": [
				{"name": "r0", "fqcn": "noop"}
			], "always": [
				{"name": "a0", "fqcn": "noop"}
			]},
			{"name": "after", "fqcn": "noop"}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

	// Every task, at every depth, lands in Nodes: tasks[0], tasks[1] (the
	// block task itself), tasks[1].block[0], tasks[1].block[1],
	// tasks[1].rescue[0], tasks[1].always[0], tasks[2].
	wantNodes := []string{
		"tasks[0]", "tasks[1]", "tasks[1].block[0]", "tasks[1].block[1]",
		"tasks[1].rescue[0]", "tasks[1].always[0]", "tasks[2]",
	}
	if len(dag.Nodes) != len(wantNodes) {
		t.Fatalf("expected %d nodes, got %d: %v", len(wantNodes), len(dag.Nodes), dag.Nodes)
	}
	for _, id := range wantNodes {
		if _, ok := dag.Nodes[id]; !ok {
			t.Errorf("expected node %q to exist", id)
		}
	}

	// The happy-path chain splices tasks[1]'s Block children into the
	// position tasks[1] occupies: tasks[0] -> tasks[1].block[0] ->
	// tasks[1].block[1] -> tasks[2]. tasks[1] itself never appears as a
	// source or target.
	if len(dag.Adjacency["tasks[0]"]) != 1 || dag.Adjacency["tasks[0]"][0].To != "tasks[1].block[0]" {
		t.Fatalf("expected tasks[0] -> tasks[1].block[0], got %v", dag.Adjacency["tasks[0]"])
	}
	if len(dag.Adjacency["tasks[1].block[0]"]) != 1 || dag.Adjacency["tasks[1].block[0]"][0].To != "tasks[1].block[1]" {
		t.Fatalf("expected tasks[1].block[0] -> tasks[1].block[1], got %v", dag.Adjacency["tasks[1].block[0]"])
	}
	if len(dag.Adjacency["tasks[1].block[1]"]) != 1 || dag.Adjacency["tasks[1].block[1]"][0].To != "tasks[2]" {
		t.Fatalf("expected tasks[1].block[1] -> tasks[2], got %v", dag.Adjacency["tasks[1].block[1]"])
	}
	if edges, ok := dag.Adjacency["tasks[1]"]; ok {
		t.Errorf("expected tasks[1] (the block task itself) to have no outgoing adjacency, got %v", edges)
	}

	// Rescue and Always never appear as a source or target in Adjacency.
	for _, id := range []string{"tasks[1].rescue[0]", "tasks[1].always[0]"} {
		if edges, ok := dag.Adjacency[id]; ok {
			t.Errorf("expected %q to have no outgoing adjacency, got %v", id, edges)
		}
	}
	for _, edges := range dag.Adjacency {
		for _, e := range edges {
			if e.To == "tasks[1].rescue[0]" || e.To == "tasks[1].always[0]" {
				t.Errorf("expected no edge to target %q, got one", e.To)
			}
		}
	}
}

// TestDAGBuilder_NeitherFQCNNorBlockIsError confirms a task with neither
// fqcn nor block is rejected with a clear, actionable error.
func TestDAGBuilder_NeitherFQCNNorBlockIsError(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-empty-task",
		"tasks": [
			{"name": "empty"}
		]
	}`)

	_, err := builder.Build(payload)
	if err == nil {
		t.Fatalf("expected an error for a task with neither fqcn nor block")
	}
	if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("expected error to name tasks[0], got: %v", err)
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected error to include the task's name, got: %v", err)
	}
}

// TestDAGBuilder_BothFQCNAndBlockIsError confirms a task with both fqcn
// and block set is rejected, since a task must be exactly one or the
// other, mirroring Ansible.
func TestDAGBuilder_BothFQCNAndBlockIsError(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-both-task",
		"tasks": [
			{"name": "confused", "fqcn": "noop", "block": [
				{"name": "child", "fqcn": "noop"}
			]}
		]
	}`)

	_, err := builder.Build(payload)
	if err == nil {
		t.Fatalf("expected an error for a task with both fqcn and block")
	}
	if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("expected error to name tasks[0], got: %v", err)
	}
}

// TestDAGBuilder_RescueWithoutBlockIsError confirms a task carrying a
// rescue: list but no block: is rejected, since rescue without a block to
// guard is meaningless.
func TestDAGBuilder_RescueWithoutBlockIsError(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-rescue-no-block",
		"tasks": [
			{"name": "orphan rescue", "fqcn": "noop", "rescue": [
				{"name": "r0", "fqcn": "noop"}
			]}
		]
	}`)

	_, err := builder.Build(payload)
	if err == nil {
		t.Fatalf("expected an error for rescue without block")
	}
	if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("expected error to name tasks[0], got: %v", err)
	}
}

// TestDAGBuilder_AlwaysWithoutBlockIsError confirms a task carrying an
// always: list but no block: is rejected, for the same reason rescue
// without block is rejected.
func TestDAGBuilder_AlwaysWithoutBlockIsError(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-always-no-block",
		"tasks": [
			{"name": "orphan always", "fqcn": "noop", "always": [
				{"name": "a0", "fqcn": "noop"}
			]}
		]
	}`)

	_, err := builder.Build(payload)
	if err == nil {
		t.Fatalf("expected an error for always without block")
	}
	if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("expected error to name tasks[0], got: %v", err)
	}
}

// TestDAGBuilder_RejectsUnsafeRunbookID is Phase 39's (Schema & Injection
// Hardening) regression test for a real finding: WorkflowDef.ID is
// embedded directly into a NATS subject string by Executor.publish
// (executor.go), so an id containing a subject-delimiter or wildcard
// character could widen or misroute a subject beyond what the publisher
// intended (FAILURE_PATTERNS.md #18). A dot is the concrete case proven
// there; "*" and ">" are NATS's own single- and multi-token wildcards.
func TestDAGBuilder_RejectsUnsafeRunbookID(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	for _, id := range []string{"billing.exfil", "wildcard*", "prefix>", "has space"} {
		payload := []byte(`{"id":"` + id + `","tasks":[{"name":"a","fqcn":"noop"}]}`)
		_, err := builder.Build(payload)
		if err == nil {
			t.Fatalf("expected id %q to be rejected", id)
		}
		if !strings.Contains(err.Error(), "invalid runbook id") {
			t.Errorf("expected an actionable error for id %q, got: %v", id, err)
		}
	}
}

// TestDAGBuilder_AllowsSafeRunbookID confirms the charset check is not
// overly strict: letters, digits, hyphens, underscores, and an absent id
// (the empty string, matching this codebase's existing lenient behavior
// for a runbook with no id: field) all still build successfully.
func TestDAGBuilder_AllowsSafeRunbookID(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	for _, id := range []string{"", "sample", "runbook-1", "runbook_1", "R9"} {
		payload := []byte(`{"id":"` + id + `","tasks":[{"name":"a","fqcn":"noop"}]}`)
		if _, err := builder.Build(payload); err != nil {
			t.Errorf("expected id %q to be accepted, got: %v", id, err)
		}
	}
}

// Cycle detection (hasCycle) has no test here: the tree-walk builder
// synthesizes Adjacency deterministically from pretasks/tasks/posttasks
// and their Block children, so a cycle is structurally unreachable
// through any authoring surface this package exposes. hasCycle stays as a
// cheap defensive guard (see dag.go), but there is deliberately no new
// user-facing way to construct one to test against.
