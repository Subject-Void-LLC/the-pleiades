package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
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
