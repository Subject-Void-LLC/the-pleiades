package engine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// fiveTaskChainDAG builds the DAG both BenchmarkExecutorRun_FiveTaskChain
// and BenchmarkAnsiblePlaybookComparable measure an equivalent workload
// against: five independent, controller-side "noop" tasks in a row.
func fiveTaskChainDAG(b *testing.B) *engine.DAG {
	b.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		b.Fatalf("failed to init CEL evaluator: %v", err)
	}
	dag, err := engine.NewBuilder(eval).Build([]byte(`{"id":"bench","tasks":[
		{"name":"t1","fqcn":"noop"},
		{"name":"t2","fqcn":"noop"},
		{"name":"t3","fqcn":"noop"},
		{"name":"t4","fqcn":"noop"},
		{"name":"t5","fqcn":"noop"}
	]}`))
	if err != nil {
		b.Fatalf("failed to build DAG: %v", err)
	}
	return dag
}

// BenchmarkExecutorRun_FiveTaskChain measures the in-process Walk-tier
// Executor's per-run overhead for a five-task runbook: DAG walk, level
// iteration, condition-less dispatch, and event publication, with no
// device targets involved. This is the Go-native number
// BenchmarkAnsiblePlaybookComparable is meant to sit next to.
func BenchmarkExecutorRun_FiveTaskChain(b *testing.B) {
	dag := fiveTaskChainDAG(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		if _, err := x.Run(context.Background(), dag); err != nil {
			b.Fatalf("Run failed: %v", err)
		}
	}
}

// BenchmarkAnsiblePlaybookComparable runs a real ansible-playbook process
// against an equivalent five-task, localhost-only, fact-gathering-disabled
// playbook, the honest per-run comparison IMPLEMENTATION.md's Phase W5
// asks for ("Benchmark against a comparable Ansible workflow"), following
// cmd/pleiades/cli_bench_test.go's own established precedent of skipping
// rather than fabricating a number when ansible-playbook is not on PATH.
func BenchmarkAnsiblePlaybookComparable(b *testing.B) {
	binary, err := exec.LookPath("ansible-playbook")
	if err != nil {
		b.Skip("ansible-playbook not found on PATH; skipping the comparison rather than fabricating a number")
	}

	const playbook = `- hosts: localhost
  connection: local
  gather_facts: no
  tasks:
    - debug: {msg: "task 1"}
    - debug: {msg: "task 2"}
    - debug: {msg: "task 3"}
    - debug: {msg: "task 4"}
    - debug: {msg: "task 5"}
`
	path := filepath.Join(b.TempDir(), "bench.yaml")
	if err := os.WriteFile(path, []byte(playbook), 0o644); err != nil {
		b.Fatalf("failed to write comparison playbook: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(binary, path, "-i", "localhost,")
		cmd.Env = append(os.Environ(), "ANSIBLE_STDOUT_CALLBACK=minimal")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("ansible-playbook failed: %v\n%s", err, out)
		}
	}
}

// BenchmarkExecutorRun_DeviceFanOut measures the in-process Executor's
// overhead fanning a single task out across 50 resolved devices: lock
// acquisition and release, worker-pool scheduling, and one event
// published per device, everything Section 13 and Section 14's
// per-device execution model actually costs at Walk tier.
func BenchmarkExecutorRun_DeviceFanOut(b *testing.B) {
	const deviceCount = 50

	devices := make([]inventory.InventoryItem, deviceCount)
	for i := range devices {
		id := inventory.DeviceID(fmt.Sprintf("host%d", i))
		devices[i] = &inventorytest.Stub{StubID: id, StubName: string(id), StubState: inventory.StateActive}
	}
	resolver := mapResolver{"all": devices}

	eval, err := engine.NewCELEvaluator()
	if err != nil {
		b.Fatalf("failed to init CEL evaluator: %v", err)
	}
	dag, err := engine.NewBuilder(eval).Build([]byte(`{"id":"bench-fanout","tasks":[{"name":"ping","fqcn":"noop","params":{"target":"all"}}]}`))
	if err != nil {
		b.Fatalf("failed to build DAG: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := engine.NewExecutor(resolver, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		result, err := x.Run(context.Background(), dag)
		if err != nil {
			b.Fatalf("Run failed: %v", err)
		}
		if result.HasErrors() {
			b.Fatalf("expected no errors, got %+v", result.Nodes)
		}
	}
}
