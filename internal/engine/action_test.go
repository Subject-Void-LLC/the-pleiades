package engine_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// TestBuiltinActionExecutor_Noop confirms the default "noop" action never
// fails, never reports Changed unless told to, and echoes its own Params
// into Stats verbatim.
func TestBuiltinActionExecutor_Noop(t *testing.T) {
	actions := engine.NewBuiltinActionExecutor()

	task := &engine.Task{Name: "check", FQCN: "noop", Params: map[string]interface{}{"needs_reboot": true}}
	result, err := actions.Execute(context.Background(), task, nil)
	if err != nil {
		t.Fatalf("expected noop to succeed, got: %v", err)
	}
	if result.Changed {
		t.Errorf("expected Changed to default to false, got true")
	}
	if !reflect.DeepEqual(result.Stats, map[string]interface{}{"needs_reboot": true}) {
		t.Errorf("expected Stats to echo Params, got %#v", result.Stats)
	}
}

// TestBuiltinActionExecutor_NoopWithChangedFlag confirms an authored
// "changed": true param is honored, the mechanism Phase W5's Release Gate
// test uses to prove a later conditional edge branches correctly.
func TestBuiltinActionExecutor_NoopWithChangedFlag(t *testing.T) {
	actions := engine.NewBuiltinActionExecutor()

	task := &engine.Task{Name: "apply", FQCN: "noop", Params: map[string]interface{}{"changed": true}}
	result, err := actions.Execute(context.Background(), task, nil)
	if err != nil {
		t.Fatalf("expected noop to succeed, got: %v", err)
	}
	if !result.Changed {
		t.Errorf("expected Changed to be true, got false")
	}
}

// TestBuiltinActionExecutor_NoopWithNoParams confirms Stats stays nil
// when a task has nothing to echo, rather than becoming an empty but
// non-nil map that would merge misleadingly into the WorkflowContext.
func TestBuiltinActionExecutor_NoopWithNoParams(t *testing.T) {
	actions := engine.NewBuiltinActionExecutor()

	task := &engine.Task{Name: "check", FQCN: "noop"}
	result, err := actions.Execute(context.Background(), task, nil)
	if err != nil {
		t.Fatalf("expected noop to succeed, got: %v", err)
	}
	if result.Stats != nil {
		t.Errorf("expected nil Stats for a task with no Params, got %#v", result.Stats)
	}
}

// TestBuiltinActionExecutor_UnknownFQCNIsExplicitError confirms any fqcn
// besides "noop" or "set_metadata" fails with an actionable, honest error
// rather than a fake success, since no transport exists yet to genuinely
// dispatch it (Phase W6).
func TestBuiltinActionExecutor_UnknownFQCNIsExplicitError(t *testing.T) {
	actions := engine.NewBuiltinActionExecutor()

	for _, fqcn := range []string{"ssh_exec", "ios_backup", "anything_else"} {
		task := &engine.Task{Name: "t", FQCN: fqcn}
		_, err := actions.Execute(context.Background(), task, nil)
		if err == nil {
			t.Fatalf("expected fqcn %q to fail with no in-process implementation, got success", fqcn)
		}
		if !strings.Contains(err.Error(), fqcn) {
			t.Errorf("expected error to name the fqcn %q, got: %v", fqcn, err)
		}
	}
}

// TestBuiltinActionExecutor_SetMetadata confirms "set_metadata" reports
// its params.data map verbatim as Stats, with IsMetadata set so Executor
// knows to surface it in RunResult.Metadata.
func TestBuiltinActionExecutor_SetMetadata(t *testing.T) {
	actions := engine.NewBuiltinActionExecutor()

	data := map[string]interface{}{"devices_patched": 3}
	task := &engine.Task{Name: "report", FQCN: "set_metadata", Params: map[string]interface{}{"data": data}}
	result, err := actions.Execute(context.Background(), task, nil)
	if err != nil {
		t.Fatalf("expected set_metadata to succeed, got: %v", err)
	}
	if !result.IsMetadata {
		t.Errorf("expected IsMetadata to be true")
	}
	if !reflect.DeepEqual(result.Stats, data) {
		t.Errorf("expected Stats to echo params.data, got %#v", result.Stats)
	}
}

// TestBuiltinActionExecutor_SetMetadataRequiresNonEmptyData confirms a
// missing or empty params.data fails loudly rather than silently
// reporting an empty metadata entry.
func TestBuiltinActionExecutor_SetMetadataRequiresNonEmptyData(t *testing.T) {
	actions := engine.NewBuiltinActionExecutor()

	cases := []struct {
		name   string
		params map[string]interface{}
	}{
		{name: "missing data", params: nil},
		{name: "wrong type", params: map[string]interface{}{"data": "not a map"}},
		{name: "empty map", params: map[string]interface{}{"data": map[string]interface{}{}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &engine.Task{Name: "report", FQCN: "set_metadata", Params: tc.params}
			_, err := actions.Execute(context.Background(), task, nil)
			if err == nil {
				t.Fatalf("expected an error, got success")
			}
			if !strings.Contains(err.Error(), "set_metadata") {
				t.Errorf("expected the error to name the fqcn, got: %v", err)
			}
		})
	}
}

// TestTaskTarget covers the default/override resolution WorkflowDef.Hosts
// documents: a task's own Params["target"] wins when it is a non-empty
// string, dag.Hosts is the fallback, and a malformed (non-string) target
// falls back exactly like an absent one, matching FAILURE_PATTERNS.md #11's
// established "malformed is indistinguishable from absent" behavior at
// every other call site.
func TestTaskTarget(t *testing.T) {
	cases := []struct {
		name   string
		hosts  string
		params map[string]interface{}
		want   string
	}{
		{"task target wins over runbook hosts", "sw1", map[string]interface{}{"target": "sw2"}, "sw2"},
		{"falls back to runbook hosts when task has none", "sw1", nil, "sw1"},
		{"falls back to runbook hosts when task target is empty", "sw1", map[string]interface{}{"target": ""}, "sw1"},
		{"neither set yields empty (controller-side task)", "", nil, ""},
		{"non-string task target falls back to runbook hosts", "sw1", map[string]interface{}{"target": []string{"sw1", "sw2"}}, "sw1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dag := &engine.DAG{Hosts: tc.hosts}
			task := &engine.Task{Params: tc.params}
			if got := engine.TaskTarget(dag, task); got != tc.want {
				t.Errorf("TaskTarget() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuiltinActionExecutor_SetMetadataNeverReportsChanged confirms
// set_metadata never reports Changed, even if a task authors a
// params.changed value the way a "noop" task would: setting metadata
// never alters device state.
func TestBuiltinActionExecutor_SetMetadataNeverReportsChanged(t *testing.T) {
	actions := engine.NewBuiltinActionExecutor()

	task := &engine.Task{
		Name: "report",
		FQCN: "set_metadata",
		Params: map[string]interface{}{
			"changed": true,
			"data":    map[string]interface{}{"x": 1},
		},
	}
	result, err := actions.Execute(context.Background(), task, nil)
	if err != nil {
		t.Fatalf("expected set_metadata to succeed, got: %v", err)
	}
	if result.Changed {
		t.Errorf("expected Changed to always be false for set_metadata, got true")
	}
}
