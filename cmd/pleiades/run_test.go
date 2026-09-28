package main

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestPrintTaskList_Parallel confirms printPlanTasks (run_text.go) prints a
// parallel task's own "parallel:" section and recurses into its children,
// mirroring how it already handles "block:". captureStdout (inventory_test.go)
// is this package's existing stdout-capture helper, reused here rather
// than duplicated.
func TestPrintTaskList_Parallel(t *testing.T) {
	dag := buildForPrint(t, "id: p\ntasks:\n  - name: fanout\n    parallel:\n      - name: p0\n        noop:\n      - name: p1\n        noop:\n", engine.TagFilter{})

	got := captureStdout(t, func() {
		printPlanTasks(planTasks(dag, dag.Tasks, "tasks"), 0)
	})

	wantLines := []string{"fanout", "parallel:", "p0", "p1"}
	for _, want := range wantLines {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

// TestPrintTaskList_BlockStillWorks is a regression guard confirming the
// switch on Task.Kind (Phase 10) preserves the pre-existing block/rescue/
// always printing behavior, not just the new parallel one.
func TestPrintTaskList_BlockStillWorks(t *testing.T) {
	dag := buildForPrint(t, "id: b\ntasks:\n  - name: risky\n    block:\n      - name: b0\n        noop:\n    rescue:\n      - name: r0\n        noop:\n    always:\n      - name: a0\n        noop:\n", engine.TagFilter{})

	got := captureStdout(t, func() {
		printPlanTasks(planTasks(dag, dag.Tasks, "tasks"), 0)
	})

	wantLines := []string{"risky", "block:", "b0", "rescue:", "r0", "always:", "a0"}
	for _, want := range wantLines {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

// buildForPrint builds payload and applies f, as loadWorld does.
func buildForPrint(t *testing.T, payload string, f engine.TagFilter) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsZero() {
		if dag, err = engine.Select(dag, f); err != nil {
			t.Fatal(err)
		}
	}
	return dag
}

// TestPrintTaskList_MarksWhatTheSelectionLeavesOut proves the plan shows
// every authored task and says which ones a --tags filter left out, so
// the printed plan and what runs cannot be read as the same list when
// they are not.
func TestPrintTaskList_MarksWhatTheSelectionLeavesOut(t *testing.T) {
	dag := buildForPrint(t, "id: s\ntasks:\n  - name: keep\n    noop:\n    tags: web\n  - name: drop\n    noop:\n    tags: db\n", engine.TagFilter{Tags: []string{"web"}})
	got := captureStdout(t, func() {
		printPlanTasks(planTasks(dag, dag.Tasks, "tasks"), 0)
	})
	if !strings.Contains(got, "drop  (not selected)") || strings.Contains(got, "keep  (not selected)") {
		t.Errorf("plan does not mark exactly the left-out task:\n%s", got)
	}
}

// TestPrintTaskList_EscapesTaskNames is the S4 regression test: a task
// name is runbook text, and a control sequence in it is shown escaped,
// never sent to the terminal.
func TestPrintTaskList_EscapesTaskNames(t *testing.T) {
	dag := buildForPrint(t, "id: e\ntasks:\n  - name: \"ok\\x1b[2J\\rfaked\"\n    noop:\n", engine.TagFilter{})
	got := captureStdout(t, func() {
		printPlanTasks(planTasks(dag, dag.Tasks, "tasks"), 0)
	})
	if strings.ContainsAny(got, "\x1b\r") || !strings.Contains(got, `\x1b[2J\rfaked`) {
		t.Errorf("task name reached the terminal unescaped: %q", got)
	}
}
