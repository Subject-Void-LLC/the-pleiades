// Tests for a task's own timeout: the key as a runbook writes it, the
// shapes it is refused on, and the executor stopping a task whose time ran
// out, beside the run-wide bound.
package engine_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestBuild_TaskTimeout: timeout is a task key in YAML (beside a method
// written as the key) and JSON alike, and is refused on a block or
// parallel group, below one second, past a day, and as anything but a
// whole number.
func TestBuild_TaskTimeout(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)
	dag, err := builder.BuildFromYAML([]byte("id: t\ntasks:\n  - name: slow\n    timeout: 90\n    noop: {}\n"))
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	jsonDAG, err := builder.Build([]byte(`{"id": "t", "tasks": [{"name": "slow", "timeout": 90, "fqcn": "noop"}]}`))
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	quoted, err := builder.BuildFromYAML([]byte("id: t\ntasks:\n  - timeout: \"90\"\n    noop: {}\n"))
	if err != nil || quoted.Tasks[0].Timeout != 90 {
		t.Errorf("a quoted number, as Ansible takes one: %v", err)
	}
	if _, err := builder.Build([]byte(`{"id": "t", "tasks": [{"timeout": 1.5, "fqcn": "noop"}]}`)); err == nil || !strings.Contains(err.Error(), "timeout 1.5; a timeout is a whole number") {
		t.Errorf("a fraction in JSON: %v", err)
	}
	if dag.Tasks[0].Timeout != 90 || jsonDAG.Tasks[0].Timeout != 90 {
		t.Errorf("timeout = %d (YAML), %d (JSON), want 90", dag.Tasks[0].Timeout, jsonDAG.Tasks[0].Timeout)
	}
	if dag.Version != jsonDAG.Version {
		t.Errorf("the same runbook in YAML and JSON hashed differently: %s, %s", dag.Version, jsonDAG.Version)
	}
	plain, err := builder.BuildFromYAML([]byte("id: t\ntasks:\n  - name: slow\n    noop: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Version == dag.Version {
		t.Error("a timeout did not change the runbook's version")
	}

	for name, tc := range map[string]struct{ runbook, want string }{
		"on a block":      {"id: t\ntasks:\n  - name: g\n    timeout: 5\n    block:\n      - noop: {}\n", "sets timeout directly on a block task"},
		"on a parallel":   {"id: t\ntasks:\n  - name: g\n    timeout: 5\n    parallel:\n      - noop: {}\n", "sets timeout directly on a parallel task"},
		"negative":        {"id: t\ntasks:\n  - timeout: -1\n    noop: {}\n", "from 1 to 86400"},
		"past a day":      {"id: t\ntasks:\n  - timeout: 86401\n    noop: {}\n", "from 1 to 86400"},
		"words":           {"id: t\ntasks:\n  - timeout: 30m\n    noop: {}\n", `timeout "30m"; a timeout is a whole number of seconds`},
		"inside a rescue": {"id: t\ntasks:\n  - block:\n      - noop: {}\n    rescue:\n      - timeout: 0x\n        noop: {}\n", `line 6: timeout "0x"`},
		"a mapping":       {"id: t\ntasks:\n  - timeout:\n      seconds: 5\n", "timeout is a YAML map"},
	} {
		if _, err := builder.BuildFromYAML([]byte(tc.runbook)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, tc.want)
		}
	}
}

// TestExecutor_TaskTimeoutStopsTheTask: a task whose own timeout runs out
// is stopped and fails, saying so; and of the task's timeout and the run's
// bound, the smaller decides.
func TestExecutor_TaskTimeoutStopsTheTask(t *testing.T) {
	device := &inventorytest.Stub{StubID: "dev", StubName: "dev", StubState: inventory.StateActive}
	resolver := mapResolver{"all": {device}}
	for _, tc := range []struct {
		name    string
		timeout int
		run     time.Duration
		want    string
	}{
		{"the task's own", 1, 0, "its timeout of 1s ran out"},
		{"the task's, smaller than the run's", 1, time.Hour, "its timeout of 1s ran out"},
		{"the run's, smaller than the task's", 3600, 200 * time.Millisecond, "its timeout of 200ms ran out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := buildDAG(t, `{"id": "timeout", "tasks": [{"name": "hangs", "fqcn": "noop", "timeout": `+strconv.Itoa(tc.timeout)+`, "params": {"target": "all"}}]}`)
			var opts []engine.ExecutorOption
			if tc.run > 0 {
				opts = append(opts, engine.WithTaskTimeout(tc.run))
			}
			x := engine.NewExecutor(resolver, ctxAwareActionExecutor{delay: time.Minute}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
			start := time.Now()
			result, err := x.Run(context.Background(), dag)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if took := time.Since(start); took > 10*time.Second {
				t.Fatalf("the task ran %s, past its timeout", took)
			}
			if len(result.Nodes) != 1 || !errors.Is(result.Nodes[0].Err, context.DeadlineExceeded) || !strings.Contains(result.Nodes[0].Err.Error(), tc.want) {
				t.Fatalf("result %+v, want a failure saying %q", result.Nodes, tc.want)
			}
		})
	}
}

// TestExecutor_ACancelledRunIsNotATimeout: a run its caller stopped does
// not blame the task's timeout.
func TestExecutor_ACancelledRunIsNotATimeout(t *testing.T) {
	device := &inventorytest.Stub{StubID: "dev", StubName: "dev", StubState: inventory.StateActive}
	dag := buildDAG(t, `{"id": "timeout", "tasks": [{"name": "hangs", "fqcn": "noop", "timeout": 3600, "params": {"target": "all"}}]}`)
	x := engine.NewExecutor(mapResolver{"all": {device}}, ctxAwareActionExecutor{delay: time.Minute}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result, _ := x.Run(ctx, dag)
	for _, n := range result.Nodes {
		if n.Err != nil && strings.Contains(n.Err.Error(), "ran out") {
			t.Errorf("a stopped run blamed the task's timeout: %v", n.Err)
		}
	}
}
