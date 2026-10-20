package engine_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
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
			{"name": "start", "noop": {}},
			{"name": "ping", "ssh_exec": {}},
			{"name": "reboot", "ios_backup": {}, "when": "stat.ping_ms > 100"}
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
			{"name": "conditional step", "noop": {}, "when": "stat.ok == true"}
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

// TestDAGBuilder_BlockRescueAlways confirms a block task's own tasks, then
// its rescue tasks, then its always tasks, splice into the chain in the
// place the block occupies, the block's own id never on it; and that each
// task records the block enclosing it and its part, and the block the nodes
// of each part, which is what the executor decides per device from.
func TestDAGBuilder_BlockRescueAlways(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-block",
		"tasks": [
			{"name": "before", "noop": {}},
			{"name": "risky", "block": [
				{"name": "b0", "ssh_exec": {}},
				{"name": "b1", "ssh_exec": {}}
			], "rescue": [
				{"name": "r0", "noop": {}}
			], "always": [
				{"name": "a0", "noop": {}}
			]},
			{"name": "after", "noop": {}}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

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

	chain := []string{"tasks[0]", "tasks[1].block[0]", "tasks[1].block[1]", "tasks[1].rescue[0]", "tasks[1].always[0]", "tasks[2]"}
	for i := 0; i+1 < len(chain); i++ {
		if edges := dag.Adjacency[chain[i]]; len(edges) != 1 || edges[0].To != chain[i+1] {
			t.Errorf("expected %s -> %s, got %v", chain[i], chain[i+1], edges)
		}
	}
	if edges, ok := dag.Adjacency["tasks[1]"]; ok {
		t.Errorf("expected tasks[1] (the block task itself) to have no outgoing adjacency, got %v", edges)
	}

	for id, part := range map[string]engine.BlockPart{
		"tasks[1].block[1]": engine.PartBlock, "tasks[1].rescue[0]": engine.PartRescue, "tasks[1].always[0]": engine.PartAlways,
	} {
		if got := dag.Scopes[id]; len(got) != 1 || got[0].Block != "tasks[1]" || got[0].Part != part {
			t.Errorf("Scopes[%s] = %v, want tasks[1]'s %s", id, got, part)
		}
	}
	if got := dag.Scopes["tasks[0]"]; got != nil {
		t.Errorf("a task in no block has scopes %v", got)
	}
	guard := dag.Guards["tasks[1]"]
	if !reflect.DeepEqual(guard.Tasks, []string{"tasks[1].block[0]", "tasks[1].block[1]"}) ||
		!reflect.DeepEqual(guard.Rescue, []string{"tasks[1].rescue[0]"}) || !reflect.DeepEqual(guard.Always, []string{"tasks[1].always[0]"}) {
		t.Errorf("Guards[tasks[1]] = %+v", guard)
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
			{"name": "confused", "noop": {}, "block": [
				{"name": "child", "noop": {}}
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
			{"name": "orphan rescue", "noop": {}, "rescue": [
				{"name": "r0", "noop": {}}
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
			{"name": "orphan always", "noop": {}, "always": [
				{"name": "a0", "noop": {}}
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
		payload := []byte(`{"id":"` + id + `","tasks":[{"name":"a","noop": {}}]}`)
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
		payload := []byte(`{"id":"` + id + `","tasks":[{"name":"a","noop": {}}]}`)
		if _, err := builder.Build(payload); err != nil {
			t.Errorf("expected id %q to be accepted, got: %v", id, err)
		}
	}
}

// Cycle detection (hasCycle) has no Builder-level test here: the
// tree-walk builder synthesizes Adjacency deterministically from
// pretasks/tasks/posttasks and their Block/Parallel children, so a cycle
// is structurally unreachable through any authoring surface this package
// exposes, Parallel's synthetic fan-out/join splice included. hasCycle
// stays as a cheap defensive guard (see dag.go); dag_internal_test.go
// (package engine, not engine_test) tests it directly against hand-built
// *DAG values, the only way to exercise a genuinely cyclic graph and to
// prove its iterative rewrite does not scale its stack usage with input
// size.

// TestDAGBuilder_Parallel confirms a parallel task compiles into a real
// fan-out/join splice: the parallel task's own id lands in Nodes (for
// coverage, exactly like a block task's own id already does), but never
// as a source or target in Adjacency; a synthetic node id+".fanout" feeds
// every child's own entry concurrently instead, and each child's own exit
// feeds a synthetic id+".join" node.
func TestDAGBuilder_Parallel(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-parallel",
		"tasks": [
			{"name": "before", "noop": {}},
			{"name": "fanout", "parallel": [
				{"name": "p0", "noop": {}},
				{"name": "p1", "noop": {}}
			]},
			{"name": "after", "noop": {}}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

	wantNodes := []string{
		"tasks[0]", "tasks[1]", "tasks[1].fanout", "tasks[1].parallel[0]", "tasks[1].parallel[1]", "tasks[1].join", "tasks[2]",
	}
	if len(dag.Nodes) != len(wantNodes) {
		t.Fatalf("expected %d nodes, got %d: %v", len(wantNodes), len(dag.Nodes), dag.Nodes)
	}
	for _, id := range wantNodes {
		if _, ok := dag.Nodes[id]; !ok {
			t.Errorf("expected node %q to exist", id)
		}
	}
	if edges, ok := dag.Adjacency["tasks[1]"]; ok {
		t.Errorf("expected the parallel task's own id tasks[1] to have no outgoing adjacency (only its fanout/join markers do), got %v", edges)
	}
	for _, edges := range dag.Adjacency {
		for _, e := range edges {
			if e.To == "tasks[1]" {
				t.Errorf("expected no edge to target tasks[1] itself, got one")
			}
		}
	}

	// before -> fanout.
	if len(dag.Adjacency["tasks[0]"]) != 1 || dag.Adjacency["tasks[0]"][0].To != "tasks[1].fanout" {
		t.Fatalf("expected tasks[0] -> tasks[1].fanout, got %v", dag.Adjacency["tasks[0]"])
	}

	// fanout -> both children, concurrently (order not guaranteed, so
	// compare as a set).
	fanoutEdges := dag.Adjacency["tasks[1].fanout"]
	if len(fanoutEdges) != 2 {
		t.Fatalf("expected tasks[1].fanout to have 2 outgoing edges, got %v", fanoutEdges)
	}
	gotTargets := map[string]bool{fanoutEdges[0].To: true, fanoutEdges[1].To: true}
	for _, want := range []string{"tasks[1].parallel[0]", "tasks[1].parallel[1]"} {
		if !gotTargets[want] {
			t.Errorf("expected tasks[1].fanout to reach %q, got %v", want, fanoutEdges)
		}
	}

	// Both children -> join.
	for _, child := range []string{"tasks[1].parallel[0]", "tasks[1].parallel[1]"} {
		edges := dag.Adjacency[child]
		if len(edges) != 1 || edges[0].To != "tasks[1].join" {
			t.Errorf("expected %s -> tasks[1].join, got %v", child, edges)
		}
	}

	// join -> after.
	if len(dag.Adjacency["tasks[1].join"]) != 1 || dag.Adjacency["tasks[1].join"][0].To != "tasks[2]" {
		t.Fatalf("expected tasks[1].join -> tasks[2], got %v", dag.Adjacency["tasks[1].join"])
	}

	// Every synthesized edge defaults to EdgeTypeOnSuccess: adding
	// EdgeType changed no existing (or new) synthesized edge's behavior.
	for source, edges := range dag.Adjacency {
		for _, e := range edges {
			if e.Type != engine.EdgeTypeOnSuccess {
				t.Errorf("expected edge %s -> %s to be EdgeTypeOnSuccess, got %v", source, e.To, e.Type)
			}
		}
	}
}

// TestDAGBuilder_ParallelWithNestedBlock confirms a parallel child that is
// itself a block task splices its own Block chain in at
// "tasks[0].parallel[i].block[j]", proving synthesizeOne's shared splice
// logic reached through the parallel path, not just the block path.
func TestDAGBuilder_ParallelWithNestedBlock(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-parallel-block",
		"tasks": [
			{"name": "fanout", "parallel": [
				{"name": "grouped", "block": [
					{"name": "b0", "noop": {}},
					{"name": "b1", "noop": {}}
				]},
				{"name": "p1", "noop": {}}
			]}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

	for _, id := range []string{"tasks[0].parallel[0].block[0]", "tasks[0].parallel[0].block[1]"} {
		if _, ok := dag.Nodes[id]; !ok {
			t.Errorf("expected node %q to exist, got nodes: %v", id, dag.Nodes)
		}
	}

	// The fanout reaches the block's own first child directly, not a
	// "tasks[0].parallel[0]" node (mirroring how a top-level block task's
	// own id never appears in Adjacency).
	fanoutEdges := dag.Adjacency["tasks[0].fanout"]
	gotTargets := map[string]bool{}
	for _, e := range fanoutEdges {
		gotTargets[e.To] = true
	}
	if !gotTargets["tasks[0].parallel[0].block[0]"] {
		t.Errorf("expected fanout to reach the block child's first task directly, got %v", fanoutEdges)
	}
	if edges, ok := dag.Adjacency["tasks[0].parallel[0]"]; ok {
		t.Errorf("expected the nested block task's own id to have no outgoing adjacency, got %v", edges)
	}
}

// TestDAGBuilder_ParallelExclusivity confirms Parallel participates in the
// same exactly-one-of-fqcn/block/parallel rule fqcn/block already had, and
// that Rescue/Always without Block is still rejected even when Parallel is
// the field actually set (Parallel stays Block-only for rescue/always).
func TestDAGBuilder_ParallelExclusivity(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	cases := map[string]string{
		"fqcn and parallel": `{"id":"x","tasks":[{"name":"bad","noop": {},"parallel":[{"name":"c","noop": {}}]}]}`,
		"block and parallel": `{"id":"x","tasks":[{"name":"bad","block":[{"name":"c","noop": {}}],
			"parallel":[{"name":"c","noop": {}}]}]}`,
		"parallel with rescue, no block": `{"id":"x","tasks":[{"name":"bad","parallel":[{"name":"c","noop": {}}],
			"rescue":[{"name":"r","noop": {}}]}]}`,
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := builder.Build([]byte(payload))
			if err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
		})
	}
}

// TestDAGBuilder_ConditionOrSecretMaskOnGroupTaskIsError is the regression
// test for the defect this codebase's own dag.go:303-306 comment already
// documented but no validation caught: a block or parallel task's own id
// never enters dag.Adjacency (synthesizeOne splices in its children's
// entry/exit instead), so a when/when_or/when_cel or secret_mask written
// directly on the group task compiled and validated cleanly and then
// never fired. Swept across both group kinds and all four annotations
// deliberately, since fixing only one instance of this shape (as an
// earlier session did for the sibling rescue/always-without-block defect)
// would repeat the exact failure this test exists to close out.
func TestDAGBuilder_ConditionOrSecretMaskOnGroupTaskIsError(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	child := `{"name":"c","noop": {}}`
	cases := map[string]string{
		"when on block":           `{"id":"x","tasks":[{"name":"bad","when":"true","block":[` + child + `]}]}`,
		"when_or on block":        `{"id":"x","tasks":[{"name":"bad","when_or":["true"],"block":[` + child + `]}]}`,
		"when_cel on block":       `{"id":"x","tasks":[{"name":"bad","when_cel":"true","block":[` + child + `]}]}`,
		"secret_mask on block":    `{"id":"x","tasks":[{"name":"bad","secret_mask":{"register":"r","fields":["f"]},"block":[` + child + `]}]}`,
		"when on parallel":        `{"id":"x","tasks":[{"name":"bad","when":"true","parallel":[` + child + `]}]}`,
		"when_or on parallel":     `{"id":"x","tasks":[{"name":"bad","when_or":["true"],"parallel":[` + child + `]}]}`,
		"when_cel on parallel":    `{"id":"x","tasks":[{"name":"bad","when_cel":"true","parallel":[` + child + `]}]}`,
		"secret_mask on parallel": `{"id":"x","tasks":[{"name":"bad","secret_mask":{"register":"r","fields":["f"]},"parallel":[` + child + `]}]}`,
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := builder.Build([]byte(payload))
			if err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
			if !strings.Contains(err.Error(), "tasks[0]") {
				t.Errorf("expected error to name tasks[0], got: %v", err)
			}
		})
	}
}

// TestDAGBuilder_ConditionOnGroupChildStillCompiles confirms the fix above
// is scoped to the group task's own annotation and does not disturb the
// documented workaround (docs/03-migrating-from-ansible.md's "Borrowed
// vocabulary" section): repeating when_cel on each child task inside a
// block or parallel group compiles cleanly, exactly as it did before this
// fix, since a child's own id is wired into the graph and its condition is
// genuinely evaluated.
func TestDAGBuilder_ConditionOnGroupChildStillCompiles(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-child-condition",
		"tasks": [
			{"name": "guarded block", "block": [
				{"name": "b0", "noop": {}, "when_cel": "true"}
			]},
			{"name": "guarded parallel", "parallel": [
				{"name": "p0", "noop": {}, "when_cel": "true"}
			]}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("expected a child's own when_cel to compile cleanly, got: %v", err)
	}
	if dag.Conditions["tasks[0].block[0]"] == nil {
		t.Errorf("expected the block child's condition to be compiled")
	}
	if dag.Conditions["tasks[1].parallel[0]"] == nil {
		t.Errorf("expected the parallel child's condition to be compiled")
	}
}

// TestDAGBuilder_Version confirms DAG.Version is a stable, deterministic
// "sha256:<hex>" digest of the fully-resolved definition: identical input
// hashes identically every time, and any real content change (not just
// whitespace/key order, which json.Marshal already normalizes) changes it.
func TestDAGBuilder_Version(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{"id":"v1","tasks":[{"name":"a","noop": {}}]}`)

	first, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}
	second, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}

	if first.Version == "" {
		t.Fatal("expected a non-empty Version")
	}
	if !strings.HasPrefix(first.Version, "sha256:") {
		t.Errorf("expected Version to be formatted \"sha256:<hex>\", got %q", first.Version)
	}
	if first.Version != second.Version {
		t.Errorf("expected identical input to produce identical Version, got %q and %q", first.Version, second.Version)
	}

	changed := []byte(`{"id":"v1","tasks":[{"name":"a","noop": {"x":1}}]}`)
	third, err := builder.Build(changed)
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}
	if third.Version == first.Version {
		t.Errorf("expected a real content change to change Version, both were %q", first.Version)
	}
}

// TestDAGBuilder_ExcessiveNestingRejected confirms block nesting beyond
// maxTaskNestingDepth (import_tasks.go) fails with a clear error rather
// than crashing the process, proving the depth bound is real and reached
// through the ordinary Build() path (RULE 0), not just import_tasks'
// own chain. deeplyNestedPayload builds a JSON payload nesting a block
// task inside itself n times.
func TestDAGBuilder_ExcessiveNestingRejected(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	t.Run("beyond the bound is rejected", func(t *testing.T) {
		_, err := builder.Build(deeplyNestedPayload(200))
		if err == nil {
			t.Fatal("expected excessive block nesting to be rejected")
		}
		if !strings.Contains(err.Error(), "max task nesting depth") {
			t.Errorf("expected a max-nesting-depth error, got: %v", err)
		}
	})

	t.Run("within the bound still builds", func(t *testing.T) {
		if _, err := builder.Build(deeplyNestedPayload(10)); err != nil {
			t.Errorf("expected 10 levels of nesting to build successfully, got: %v", err)
		}
	})
}

// deeplyNestedPayload returns a runbook JSON payload with a block task
// nested n levels deep, innermost holding one leaf "noop" task. Shared by
// TestDAGBuilder_ExcessiveNestingRejected above and dag_fuzz_test.go's
// FuzzDAGBuilder seed corpus, so the fuzzer exercises the identical
// adversarial shape the targeted test already pins down.
func deeplyNestedPayload(n int) []byte {
	inner := `{"name":"leaf","noop": {}}`
	for i := 0; i < n; i++ {
		inner = `{"name":"level","block":[` + inner + `]}`
	}
	return []byte(`{"id":"deep","tasks":[` + inner + `]}`)
}

// TestDAGBuilder_LargeFlatTaskListDoesNotCrash is the real, end-to-end
// (RULE 0) proof that hasCycle's iterative rewrite scales with a large
// flat tasks: list (no nesting, so maxTaskNestingDepth does not bound it)
// through the actual Build() path a runbook file goes through, not just
// dag_internal_test.go's direct hasCycle unit tests.
func TestDAGBuilder_LargeFlatTaskListDoesNotCrash(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	const n = 20000
	var sb strings.Builder
	sb.WriteString(`{"id":"large","tasks":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"name":"t","noop": {}}`)
	}
	sb.WriteString(`]}`)

	dag, err := builder.Build([]byte(sb.String()))
	if err != nil {
		t.Fatalf("failed to build a large flat task list: %v", err)
	}
	if len(dag.Nodes) != n {
		t.Fatalf("expected %d nodes, got %d", n, len(dag.Nodes))
	}
}
