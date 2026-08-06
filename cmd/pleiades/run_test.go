package main

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// TestPrintTaskList_Parallel confirms printTaskList (run.go) prints a
// parallel task's own "parallel:" section and recurses into its children,
// mirroring how it already handles "block:". captureStdout (inventory_test.go)
// is this package's existing stdout-capture helper, reused here rather
// than duplicated.
func TestPrintTaskList_Parallel(t *testing.T) {
	tasks := []engine.Task{
		{Name: "fanout", Parallel: []engine.Task{
			{Name: "p0", FQCN: "noop"},
			{Name: "p1", FQCN: "noop"},
		}},
	}

	got := captureStdout(t, func() {
		printTaskList(tasks, 0)
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
	tasks := []engine.Task{
		{Name: "risky", Block: []engine.Task{
			{Name: "b0", FQCN: "noop"},
		}, Rescue: []engine.Task{
			{Name: "r0", FQCN: "noop"},
		}, Always: []engine.Task{
			{Name: "a0", FQCN: "noop"},
		}},
	}

	got := captureStdout(t, func() {
		printTaskList(tasks, 0)
	})

	wantLines := []string{"risky", "block:", "b0", "rescue:", "r0", "always:", "a0"}
	for _, want := range wantLines {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}
