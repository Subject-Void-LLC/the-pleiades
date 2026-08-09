package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// FuzzExecutorRun proves Executor.Run never panics and never deadlocks
// across arbitrary acyclic graph shapes, including diamonds and long
// chains (Phase W5's own Fuzz/Stress Test requirement), reusing
// synthesizeAcyclicDAG (level_iterator_fuzz_test.go) so the same graph
// generator exercises both the graph-ordering primitive and the full
// execution path built on top of it. Every synthesized node is a
// controller-side "noop" task with no condition, so a clean run must
// finish with exactly one successful result per node and no error at all;
// a context.WithTimeout catches a hang (e.g. a worker-pool bug that never
// releases a semaphore slot) as a test failure instead of stalling the
// fuzzer.
func FuzzExecutorRun(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{5, 0, 0, 0, 0})
	f.Add([]byte{4, 0, 0, 1, 3, 0, 1, 2, 3, 4})
	f.Add([]byte{16, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})

	f.Fuzz(func(t *testing.T, data []byte) {
		dag := synthesizeAcyclicDAG(data)

		x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		result, err := x.Run(ctx, dag)
		if err != nil {
			t.Fatalf("Run failed on a synthesized acyclic graph: %v", err)
		}
		if result.HasErrors() {
			t.Fatalf("expected every synthesized noop node to succeed, got %+v", result.Nodes)
		}
		if len(result.Nodes) != len(dag.Nodes) {
			t.Fatalf("expected %d node results (one per synthesized node), got %d", len(dag.Nodes), len(result.Nodes))
		}
	})
}

// FuzzRegisterMaskPath proves resolveRegisterMaskPath's dotted-path walk
// (executor_secrets.go, reached through register_mask) never panics on an
// arbitrary path string, run through the real Executor against a fixed,
// two-level-nested Stats shape: deeply nested paths, paths with
// leading/trailing/doubled dots, paths naming a real key at the wrong
// depth (walking through a leaf value), and empty segments must all
// either resolve, benignly skip, or fail the node with a clear error, but
// never crash the process that would otherwise be running a user's
// runbook.
func FuzzRegisterMaskPath(f *testing.F) {
	seeds := []string{
		"",
		".",
		"..",
		"a",
		"a.b",
		"a.b.c",
		".a",
		"a.",
		"a..b",
		"secret",
		"secret.oops",
		"parent",
		"parent.nested",
		"parent.nested.too_deep",
		"parent.does_not_exist",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	eval, err := engine.NewCELEvaluator()
	if err != nil {
		f.Fatal(err)
	}
	builder := engine.NewBuilder(eval)

	f.Fuzz(func(t *testing.T, path string) {
		task := map[string]any{
			"name":          "mark",
			"fqcn":          "noop",
			"register":      "creds",
			"register_mask": []string{path},
			"params": map[string]any{
				// "secret" is a long-enough leaf string (a non-map
				// intermediate, if path tries to descend through it);
				// "parent.nested" is the one genuinely resolvable nested
				// path among the seeds above.
				"secret": "a-long-enough-leaf-value",
				"parent": map[string]any{
					"nested": "a-long-enough-nested-value",
				},
			},
		}
		payload, err := json.Marshal(map[string]any{
			"id":    "fuzz-register-mask",
			"tasks": []any{task},
		})
		if err != nil {
			t.Fatalf("marshaling fuzz runbook: %v", err)
		}

		dag, err := builder.Build(payload)
		if err != nil {
			return // an invalid path string rejected at build time is fine
		}

		x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = x.Run(ctx, dag) // a node failure is an acceptable outcome; a panic or hang is not
	})
}
