// Package engine_test: tests of the check_mode runbook key.
package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// buildYAML compiles a YAML runbook through the real Builder, returning
// the error rather than failing, so a test can assert on a refusal.
func buildYAML(t *testing.T, payload string) (*engine.DAG, error) {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	return engine.NewBuilder(eval).BuildFromYAML([]byte(payload))
}

// TestCheckModeKey_Spellings covers what the check_mode key accepts and
// refuses, in YAML and in JSON: Ansible's spellings of true narrow, and
// every spelling of false, a template, and any other shape is refused
// with its reason.
func TestCheckModeKey_Spellings(t *testing.T) {
	for _, value := range []string{"true", "True", "yes", "YES", "on", "y", "t", `"yes"`} {
		t.Run("accepts "+value, func(t *testing.T) {
			dag, err := buildYAML(t, "id: spell\ntasks:\n  - name: a\n    fqcn: noop\n    check_mode: "+value+"\n")
			if err != nil {
				t.Fatalf("check_mode: %s refused: %v", value, err)
			}
			if !dag.Nodes["tasks[0]"].CheckMode {
				t.Errorf("check_mode: %s did not mark the task", value)
			}
		})
	}
	for value, want := range map[string]string{
		"false":           "check_mode: false is refused",
		"no":              "check_mode: false is refused",
		"off":             "check_mode: false is refused",
		"0":               "check_mode: false is refused",
		`"{{ dry_run }}"`: "not a template",
		`[true]`:          "got a YAML list",
		`{on: true}`:      "got a YAML map",
		"1":               "must be true",
		"maybe":           "must be true",
		`""`:              "must be true",
	} {
		t.Run("refuses "+value, func(t *testing.T) {
			_, err := buildYAML(t, "id: spell\ntasks:\n  - name: a\n    fqcn: noop\n    check_mode: "+value+"\n")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("check_mode: %s = %v, want a refusal containing %q", value, err, want)
			}
		})
	}
	t.Run("JSON", func(t *testing.T) {
		eval, _ := engine.NewCELEvaluator()
		b := engine.NewBuilder(eval)
		if _, err := b.Build([]byte(`{"id":"j","tasks":[{"name":"a","fqcn":"noop","check_mode":true}]}`)); err != nil {
			t.Errorf("JSON check_mode true refused: %v", err)
		}
		if _, err := b.Build([]byte(`{"id":"j","tasks":[{"name":"a","fqcn":"noop","check_mode":false}]}`)); err == nil || !strings.Contains(err.Error(), "is refused") {
			t.Errorf("JSON check_mode false = %v, want refused", err)
		}
		if _, err := b.Build([]byte(`{"id":"j","check_mode":"no","tasks":[{"name":"a","fqcn":"noop"}]}`)); err == nil {
			t.Error(`JSON runbook-level check_mode "no" was accepted`)
		}
	})
}

// TestCheckModeKey_ReachesEveryTaskItCovers proves the key is copied down
// to every task it covers: from the runbook to every task, and from a
// block to its block, rescue and always tasks and anything nested in
// them, and nowhere else.
func TestCheckModeKey_ReachesEveryTaskItCovers(t *testing.T) {
	dag, err := buildYAML(t, `id: cover
tasks:
  - name: outside
    fqcn: noop
  - name: group
    check_mode: yes
    block:
      - name: inner
        fqcn: noop
      - name: nested
        parallel:
          - name: deep
            fqcn: noop
    rescue:
      - name: recover
        fqcn: noop
    always:
      - name: cleanup
        fqcn: noop
`)
	if err != nil {
		t.Fatal(err)
	}
	for id, task := range dag.Nodes {
		if task.FQCN == "" {
			continue
		}
		want := task.Name != "outside"
		if bool(task.CheckMode) != want {
			t.Errorf("%s (%s) check_mode = %v, want %v", id, task.Name, task.CheckMode, want)
		}
	}

	whole, err := buildYAML(t, "id: whole\ncheck_mode: true\npretasks:\n  - name: p\n    fqcn: noop\ntasks:\n  - name: a\n    fqcn: noop\nposttasks:\n  - name: z\n    fqcn: noop\n")
	if err != nil {
		t.Fatal(err)
	}
	if !whole.CheckMode {
		t.Error("a runbook-level check_mode did not mark the DAG")
	}
	for id, task := range whole.Nodes {
		if task.FQCN != "" && !task.CheckMode {
			t.Errorf("%s is not covered by the runbook-level check_mode", id)
		}
	}
}

// TestCheckModeKey_ImportTasksInherit proves an imported file's tasks
// inherit the check_mode of the import_tasks task that pulled them in.
func TestCheckModeKey_ImportTasksInherit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "steps.yaml"), []byte("- name: imported\n  fqcn: noop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.yaml")
	if err := os.WriteFile(main, []byte("id: imports\ntasks:\n  - name: pull\n    fqcn: import_tasks\n    check_mode: true\n    params:\n      file: steps.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	eval, _ := engine.NewCELEvaluator()
	dag, err := engine.NewBuilder(eval).BuildFromYAMLFile(main)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, task := range dag.Nodes {
		if task.Name == "imported" {
			found = true
			if !task.CheckMode {
				t.Error("an imported task did not inherit its import_tasks task's check_mode")
			}
		}
	}
	if !found {
		t.Fatal("the imported task is not in the DAG")
	}
}

// TestRunbookKeys_MatchWorkflowDef keeps RunbookKeys and WorkflowDef's
// struct tags one set: a field added without a key would be refused on
// every runbook that used it, and a key without a field would be
// accepted and dropped.
func TestRunbookKeys_MatchWorkflowDef(t *testing.T) {
	var fromTags []string
	typ := reflect.TypeOf(engine.WorkflowDef{})
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
		fromTags = append(fromTags, name)
	}
	var fromMap []string
	for k := range engine.RunbookKeys {
		fromMap = append(fromMap, k)
	}
	slices.Sort(fromTags)
	slices.Sort(fromMap)
	if !slices.Equal(fromTags, fromMap) {
		t.Errorf("RunbookKeys = %v, WorkflowDef's yaml tags = %v", fromMap, fromTags)
	}
}

// TestRunbookKeys_UnknownTopLevelKeyRefused is FAILURE_PATTERNS 252's
// regression test: a top-level key the runbook format does not have is
// refused, naming it, and a near miss is corrected.
func TestRunbookKeys_UnknownTopLevelKeyRefused(t *testing.T) {
	_, err := buildYAML(t, "id: typo\nchek_mode: true\ntasks:\n  - name: a\n    fqcn: noop\n")
	if err == nil || !strings.Contains(err.Error(), `"chek_mode" (did you mean "check_mode"?)`) {
		t.Errorf("a misspelled check_mode = %v, want it refused with a suggestion", err)
	}
	_, err = buildYAML(t, "id: ansible\nbecome: true\ntasks:\n  - name: a\n    fqcn: noop\n")
	if err == nil || !strings.Contains(err.Error(), `unknown top-level runbook key "become"`) {
		t.Errorf("an unsupported Ansible key = %v, want it refused by name", err)
	}
	eval, _ := engine.NewCELEvaluator()
	if _, err := engine.NewBuilder(eval).Build([]byte(`{"id":"j","gather_facts":false,"tasks":[{"name":"a","fqcn":"noop"}]}`)); err == nil || !strings.Contains(err.Error(), "gather_facts") {
		t.Errorf("JSON unknown key = %v, want it refused by name", err)
	}
}

// TestTaskMode is the narrowing rule the executor and validation share.
func TestTaskMode(t *testing.T) {
	plain, checked := &engine.Task{}, &engine.Task{CheckMode: true}
	whole := &engine.DAG{CheckMode: true}
	for _, tc := range []struct {
		run  collection.Mode
		dag  *engine.DAG
		task *engine.Task
		want collection.Mode
	}{
		{collection.ModeExecute, nil, plain, collection.ModeExecute},
		{collection.ModeExecute, nil, checked, collection.ModeCheck},
		{"", nil, checked, collection.ModeCheck},
		{collection.ModeExecute, whole, plain, collection.ModeCheck},
		{collection.ModeExecute, whole, nil, collection.ModeCheck},
		{collection.ModeCheck, nil, plain, collection.ModeCheck},
		{"bogus", nil, checked, "bogus"},
	} {
		if got := engine.TaskMode(tc.run, tc.dag, tc.task); got != tc.want {
			t.Errorf("TaskMode(%q, dag check_mode %v, task %+v) = %q, want %q", tc.run, tc.dag != nil, tc.task, got, tc.want)
		}
	}
}

// TestCheckable covers the plan-time answer for each kind of action, and
// for a method whose check covers only some calls: its CheckCall answers
// for the call's own parameters, and its reason reaches the answer.
func TestCheckable(t *testing.T) {
	can := registerCheckedMethod(t, "checkable", true, false, false)
	cannot := registerCheckedMethod(t, "notcheckable", false, false, false)
	const partly = "enginecheck.partly_call"
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{
		Name: partly,
		Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"}, SupportsCheck: true,
		},
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		},
		Check: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		},
		CheckCall: func(params map[string]any) error {
			if params["creates"] == nil {
				return fmt.Errorf("wrapped: %w", collection.CannotCheck("no guard to read"))
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		fqcn   string
		params map[string]any
		want   bool
		reason string
	}{
		{"noop", nil, true, ""},
		{"set_metadata", nil, true, ""},
		{"pleiades.builtin.set_metadata", nil, true, ""},
		{can.name, nil, true, ""},
		{cannot.name, nil, false, "declares no check support"},
		{"ssh_exec", nil, false, "neither"},
		{"nosuch.method", nil, false, "neither"},
		{partly, map[string]any{"creates": "/x"}, true, ""},
		{partly, map[string]any{"cmd": "/bin/true"}, false, partly + " cannot check this call: no guard to read"},
		{partly, nil, false, "no guard to read"},
	} {
		got, reason := engine.Checkable(tc.fqcn, tc.params)
		if got != tc.want || !strings.Contains(reason, tc.reason) || (!got && reason == "") {
			t.Errorf("Checkable(%q, %v) = %v (%q), want %v with a reason containing %q", tc.fqcn, tc.params, got, reason, tc.want, tc.reason)
		}
	}
}

// entriesJournal keeps every journal entry, so a test can see which nodes
// were recorded.
type entriesJournal struct {
	mu      sync.Mutex
	entries []engine.JournalEntry
}

// Record keeps entries.
func (j *entriesJournal) Record(_ context.Context, entries []engine.JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, entries...)
	return nil
}

// TestCheckModeKey_ChecksOneTaskOfARealRun is the key's core claim: in a
// real run, a task carrying check_mode runs its Check and never its
// Invoke, the task beside it runs for real, and only the real one is
// journaled.
func TestCheckModeKey_ChecksOneTaskOfARealRun(t *testing.T) {
	checked := registerCheckedMethod(t, "keychecked", true, true, false)
	real := registerCheckedMethod(t, "keyreal", true, true, false)
	dag, err := buildYAML(t, "id: mixed\ntasks:\n  - name: dry\n    fqcn: "+checked.name+"\n    check_mode: true\n  - name: wet\n    fqcn: "+real.name+"\n")
	if err != nil {
		t.Fatal(err)
	}
	journal := &entriesJournal{}
	result, err := newCheckExecutor(mapResolver{}, engine.WithJournal(journal)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Mode != collection.ModeExecute {
		t.Errorf("RunResult.Mode = %q, want a real run", result.Mode)
	}
	if checked.invokes.Load() != 0 || checked.checks.Load() != 1 {
		t.Errorf("the check_mode task ran Invoke %d and Check %d time(s), want 0 and 1", checked.invokes.Load(), checked.checks.Load())
	}
	if real.invokes.Load() != 1 || real.checks.Load() != 0 {
		t.Errorf("the other task ran Invoke %d and Check %d time(s), want 1 and 0", real.invokes.Load(), real.checks.Load())
	}
	if dry := nodeByID(t, result, "tasks[0]"); !dry.Checked || !dry.Changed {
		t.Errorf("the check_mode task's result = %+v, want Checked with its prediction", dry)
	}
	if wet := nodeByID(t, result, "tasks[1]"); wet.Checked {
		t.Error("the real task's result says Checked")
	}
	for _, e := range journal.entries {
		if e.NodeID == "tasks[0]" {
			t.Errorf("the checked task was journaled: %+v", e)
		}
	}
	if !slices.ContainsFunc(journal.entries, func(e engine.JournalEntry) bool { return e.NodeID == "tasks[1]" }) {
		t.Error("the real task was not journaled, so the assertion above proves nothing")
	}
}

// TestCheckModeKey_RunbookLevelMakesTheRunACheck proves a runbook-level
// check_mode turns a run started for real into a check: nothing is
// invoked, the result says check, and nothing is journaled.
func TestCheckModeKey_RunbookLevelMakesTheRunACheck(t *testing.T) {
	m := registerCheckedMethod(t, "keywhole", true, true, false)
	dag, err := buildYAML(t, "id: whole\ncheck_mode: true\ntasks:\n  - name: a\n    fqcn: "+m.name+"\n")
	if err != nil {
		t.Fatal(err)
	}
	journal := &recordingJournal{}
	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeExecute), engine.WithJournal(journal)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Mode != collection.ModeCheck {
		t.Errorf("RunResult.Mode = %q, want check", result.Mode)
	}
	if m.invokes.Load() != 0 || m.checks.Load() != 1 {
		t.Errorf("Invoke %d, Check %d, want 0 and 1", m.invokes.Load(), m.checks.Load())
	}
	if journal.records.Load() != 0 {
		t.Error("a runbook-level check wrote the journal")
	}
}

// TestConditionReads covers which registered results a condition reads.
func TestConditionReads(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cond    engine.Conditional
		want    []string
		dynamic bool
	}{
		{"select", engine.Conditional{When: engine.StringList{"stat.a.changed"}}, []string{"a"}, false},
		{"nodes", engine.Conditional{WhenCEL: `nodes.b["dev"].ok`}, []string{"b"}, false},
		{"literal index", engine.Conditional{WhenOr: engine.StringList{`stat["c"].ok`, "vars.x == 1"}}, []string{"c"}, false},
		{"has", engine.Conditional{WhenCEL: "has(stat.d)"}, []string{"d"}, false},
		{"several", engine.Conditional{When: engine.StringList{"stat.a.ok", "stat.b.ok && nodes.a.ok"}}, []string{"a", "b"}, false},
		{"result", engine.Conditional{WhenCEL: "result.e.json.priority == '1 - Critical'"}, []string{"e"}, false},
		{"computed index", engine.Conditional{WhenCEL: "stat[vars.name].ok"}, nil, true},
		{"macro over the map", engine.Conditional{WhenCEL: "stat.exists(k, k == 'x')"}, nil, true},
		{"none", engine.Conditional{When: engine.StringList{"vars.go == true"}}, nil, false},
		{"empty", engine.Conditional{}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names, dynamic, err := engine.ConditionReads(tc.cond)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(names, tc.want) || dynamic != tc.dynamic {
				t.Errorf("ConditionReads = %v, dynamic %v; want %v, dynamic %v", names, dynamic, tc.want, tc.dynamic)
			}
		})
	}
	if _, _, err := engine.ConditionReads(engine.Conditional{WhenCEL: "stat.("}); err == nil {
		t.Error("an unparsable condition was accepted")
	}
}
