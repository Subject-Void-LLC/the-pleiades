package engine_test

import (
	"strings"
	"testing"
)

// TestModuleAsKeySugar_YAML_MatchesExplicit proves module-as-key sugar
// (net.cli.command: {...}) and today's explicit fqcn:/params: shape
// compile to the identical DAG.
func TestModuleAsKeySugar_YAML_MatchesExplicit(t *testing.T) {
	builder := newTestBuilder(t)

	sugar := []byte(`
id: sugar
tasks:
  - name: show version
    net.cli.command:
      command: "show version"
    register: pre_facts
`)
	explicit := []byte(`
id: sugar
tasks:
  - name: show version
    fqcn: net.cli.command
    params:
      command: "show version"
    register: pre_facts
`)

	sugarDAG, err := builder.BuildFromYAML(sugar)
	if err != nil {
		t.Fatalf("failed to build DAG from sugar syntax: %v", err)
	}
	explicitDAG, err := builder.BuildFromYAML(explicit)
	if err != nil {
		t.Fatalf("failed to build DAG from explicit syntax: %v", err)
	}

	sugarTask := sugarDAG.Nodes["tasks[0]"]
	explicitTask := explicitDAG.Nodes["tasks[0]"]
	if sugarTask.FQCN != explicitTask.FQCN {
		t.Errorf("fqcn mismatch: sugar=%q explicit=%q", sugarTask.FQCN, explicitTask.FQCN)
	}
	if sugarTask.Params["command"] != explicitTask.Params["command"] {
		t.Errorf("params mismatch: sugar=%v explicit=%v", sugarTask.Params, explicitTask.Params)
	}
	if sugarTask.Register != explicitTask.Register {
		t.Errorf("register mismatch: sugar=%q explicit=%q", sugarTask.Register, explicitTask.Register)
	}
}

// TestModuleAsKeySugar_JSON_MatchesExplicit is the JSON-path counterpart
// of TestModuleAsKeySugar_YAML_MatchesExplicit, proving the JSON
// normalizer produces the same rewrite as the YAML one.
func TestModuleAsKeySugar_JSON_MatchesExplicit(t *testing.T) {
	builder := newTestBuilder(t)

	sugar := []byte(`{
		"id": "sugar",
		"tasks": [
			{"name": "show version", "net.cli.command": {"command": "show version"}, "register": "pre_facts"}
		]
	}`)
	explicit := []byte(`{
		"id": "sugar",
		"tasks": [
			{"name": "show version", "fqcn": "net.cli.command", "params": {"command": "show version"}, "register": "pre_facts"}
		]
	}`)

	sugarDAG, err := builder.Build(sugar)
	if err != nil {
		t.Fatalf("failed to build DAG from sugar JSON: %v", err)
	}
	explicitDAG, err := builder.Build(explicit)
	if err != nil {
		t.Fatalf("failed to build DAG from explicit JSON: %v", err)
	}

	sugarTask := sugarDAG.Nodes["tasks[0]"]
	explicitTask := explicitDAG.Nodes["tasks[0]"]
	if sugarTask.FQCN != explicitTask.FQCN {
		t.Errorf("fqcn mismatch: sugar=%q explicit=%q", sugarTask.FQCN, explicitTask.FQCN)
	}
	if sugarTask.Params["command"] != explicitTask.Params["command"] {
		t.Errorf("params mismatch: sugar=%v explicit=%v", sugarTask.Params, explicitTask.Params)
	}
}

// TestModuleAsKeySugar_NoParams proves a module written with sugar syntax
// and no arguments at all (`noop:` with a null value) decodes with an
// empty FQCN/no Params, not an error.
func TestModuleAsKeySugar_NoParams(t *testing.T) {
	builder := newTestBuilder(t)

	dag, err := builder.BuildFromYAML([]byte(`
id: sugar-no-params
tasks:
  - name: do nothing
    noop:
`))
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}
	task := dag.Nodes["tasks[0]"]
	if task.FQCN != "noop" {
		t.Errorf("expected fqcn %q, got %q", "noop", task.FQCN)
	}
	if len(task.Params) != 0 {
		t.Errorf("expected no params, got %v", task.Params)
	}
}

// TestModuleAsKeySugar_NestedBlockRescueAlways proves sugar syntax is
// rewritten at every block/rescue/always nesting depth, not just the
// top-level pretasks/tasks/posttasks lists.
func TestModuleAsKeySugar_NestedBlockRescueAlways(t *testing.T) {
	builder := newTestBuilder(t)

	dag, err := builder.BuildFromYAML([]byte(`
id: sugar-nested
tasks:
  - name: guarded
    block:
      - name: risky
        net.ios.config:
          lines: ["boot system flash:image.bin"]
    rescue:
      - name: on failure
        pleiades.builtin.set_metadata:
          data:
            failed: true
    always:
      - name: cleanup
        noop:
`))
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}
	if got := dag.Nodes["tasks[0].block[0]"].FQCN; got != "net.ios.config" {
		t.Errorf("block child fqcn mismatch: got %q", got)
	}
	if got := dag.Nodes["tasks[0].rescue[0]"].FQCN; got != "pleiades.builtin.set_metadata" {
		t.Errorf("rescue child fqcn mismatch: got %q", got)
	}
	if got := dag.Nodes["tasks[0].always[0]"].FQCN; got != "noop" {
		t.Errorf("always child fqcn mismatch: got %q", got)
	}
}

// TestModuleAsKeySugar_MixedWithExplicitSyntax proves sugar and explicit
// fqcn:/params: syntax may coexist task by task within one runbook.
func TestModuleAsKeySugar_MixedWithExplicitSyntax(t *testing.T) {
	builder := newTestBuilder(t)

	dag, err := builder.BuildFromYAML([]byte(`
id: sugar-mixed
tasks:
  - name: sugar task
    net.cli.command:
      command: "show version"
  - name: explicit task
    fqcn: noop
`))
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}
	if got := dag.Nodes["tasks[0]"].FQCN; got != "net.cli.command" {
		t.Errorf("expected sugar task fqcn %q, got %q", "net.cli.command", got)
	}
	if got := dag.Nodes["tasks[1]"].FQCN; got != "noop" {
		t.Errorf("expected explicit task fqcn %q, got %q", "noop", got)
	}
}

// TestModuleAsKeySugar_AmbiguousKeys_Errors proves a task with two
// unrecognized keys (ambiguous: which one is the module?) is rejected
// with a clear error, not silently resolved to one of them.
func TestModuleAsKeySugar_AmbiguousKeys_Errors(t *testing.T) {
	builder := newTestBuilder(t)

	_, err := builder.BuildFromYAML([]byte(`
id: sugar-ambiguous
tasks:
  - name: confused
    net.cli.command:
      command: "show version"
    net.ios.config:
      lines: ["boot system flash:image.bin"]
`))
	if err == nil {
		t.Fatal("expected an error for a task with two unrecognized keys")
	}
	if !strings.Contains(err.Error(), "multiple unrecognized keys") {
		t.Errorf("expected error to name the ambiguity, got: %v", err)
	}
}

// TestModuleAsKeySugar_ConflictsWithExplicitFQCN_Errors proves a task
// combining sugar syntax with an explicit fqcn: is rejected rather than
// silently picking one, since the two are contradictory hints about what
// this task calls.
func TestModuleAsKeySugar_ConflictsWithExplicitFQCN_Errors(t *testing.T) {
	builder := newTestBuilder(t)

	_, err := builder.BuildFromYAML([]byte(`
id: sugar-conflict-fqcn
tasks:
  - name: confused
    fqcn: noop
    net.cli.command:
      command: "show version"
`))
	if err == nil {
		t.Fatal("expected an error for a task combining fqcn: with sugar syntax")
	}
	if !strings.Contains(err.Error(), "fqcn:") {
		t.Errorf("expected error to name the fqcn: conflict, got: %v", err)
	}
}

// TestModuleAsKeySugar_ConflictsWithExplicitBlock_Errors is the block:
// counterpart of TestModuleAsKeySugar_ConflictsWithExplicitFQCN_Errors.
func TestModuleAsKeySugar_ConflictsWithExplicitBlock_Errors(t *testing.T) {
	builder := newTestBuilder(t)

	_, err := builder.BuildFromYAML([]byte(`
id: sugar-conflict-block
tasks:
  - name: confused
    block:
      - name: child
        fqcn: noop
    net.cli.command:
      command: "show version"
`))
	if err == nil {
		t.Fatal("expected an error for a task combining block: with sugar syntax")
	}
	if !strings.Contains(err.Error(), "block:") {
		t.Errorf("expected error to name the block: conflict, got: %v", err)
	}
}

// TestModuleAsKeySugar_ConflictsWithExplicitParallel_Errors is the
// parallel: counterpart of TestModuleAsKeySugar_ConflictsWithExplicitBlock_Errors,
// proving ReservedTaskKeys (task_syntax.go) recognizes "parallel" the same
// way it already recognizes "block", not just at the DAG-validation layer
// (tasktree.go's validateTask) but at the sugar-normalization layer this
// file owns: without this, the sugar rewriter would misread parallel:'s
// own list as an unrecognized module-as-key value instead.
func TestModuleAsKeySugar_ConflictsWithExplicitParallel_Errors(t *testing.T) {
	builder := newTestBuilder(t)

	_, err := builder.BuildFromYAML([]byte(`
id: sugar-conflict-parallel
tasks:
  - name: confused
    parallel:
      - name: child
        fqcn: noop
    net.cli.command:
      command: "show version"
`))
	if err == nil {
		t.Fatal("expected an error for a task combining parallel: with sugar syntax")
	}
	if !strings.Contains(err.Error(), "parallel:") {
		t.Errorf("expected error to name the parallel: conflict, got: %v", err)
	}
}

// TestModuleAsKeySugar_NestedInParallel proves module-as-key sugar is
// rewritten inside a parallel: child too, mirroring
// TestModuleAsKeySugar_NestedBlockRescueAlways for block/rescue/always.
func TestModuleAsKeySugar_NestedInParallel(t *testing.T) {
	builder := newTestBuilder(t)

	dag, err := builder.BuildFromYAML([]byte(`
id: sugar-in-parallel
tasks:
  - name: fanout
    parallel:
      - name: child
        net.cli.command:
          command: "show version"
`))
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}

	task, ok := dag.Nodes["tasks[0].parallel[0]"]
	if !ok {
		t.Fatalf("expected node tasks[0].parallel[0] to exist, got nodes: %v", dag.Nodes)
	}
	if task.FQCN != "net.cli.command" {
		t.Errorf("expected fqcn %q, got %q", "net.cli.command", task.FQCN)
	}
	if task.Params["command"] != "show version" {
		t.Errorf("expected params.command %q, got %v", "show version", task.Params)
	}
}

// TestModuleAsKeySugar_NonMapValue_Errors proves a module-as-key value
// that isn't a map of arguments (a scalar or a list) is rejected rather
// than silently coerced into something Params can't sensibly represent.
func TestModuleAsKeySugar_NonMapValue_Errors(t *testing.T) {
	builder := newTestBuilder(t)

	_, err := builder.BuildFromYAML([]byte(`
id: sugar-non-map
tasks:
  - name: bad
    net.cli.command: "show version"
`))
	if err == nil {
		t.Fatal("expected an error for a non-map module value")
	}
	if !strings.Contains(err.Error(), "must be a map of arguments") {
		t.Errorf("expected error to explain the non-map value, got: %v", err)
	}
}

// TestModuleAsKeySugar_JSON_AmbiguousKeys_Errors is the JSON-path
// counterpart of TestModuleAsKeySugar_AmbiguousKeys_Errors, proving the
// JSON normalizer enforces the same ambiguity rule.
func TestModuleAsKeySugar_JSON_AmbiguousKeys_Errors(t *testing.T) {
	builder := newTestBuilder(t)

	_, err := builder.Build([]byte(`{
		"id": "sugar-ambiguous",
		"tasks": [
			{"name": "confused", "net.cli.command": {"command": "x"}, "net.ios.config": {"lines": []}}
		]
	}`))
	if err == nil {
		t.Fatal("expected an error for a task with two unrecognized keys")
	}
	if !strings.Contains(err.Error(), "multiple unrecognized keys") {
		t.Errorf("expected error to name the ambiguity, got: %v", err)
	}
}
