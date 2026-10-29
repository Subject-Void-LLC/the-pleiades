// Tests of the report's not_run entries: which planned tasks get one, in
// what order, and the reason each kind of stop gives.
package main

import (
	"testing"
)

// TestAddNotRun: every selected leaf with no result gets one entry, in the
// plan's order (a block's tasks, then its rescue, then its always, and a
// parallel's tasks), after what ran; a group itself, a task --tags left out,
// and a task that ran get none.
func TestAddNotRun(t *testing.T) {
	rep := &runReport{
		Plan: []planSection{
			{Section: "pretasks", Tasks: []planTask{{ID: "pretasks[0]", Method: "noop", Selected: true}}},
			{Section: "tasks", Tasks: []planTask{
				{ID: "tasks[0]", Name: "ran", Method: "noop", Selected: true},
				{ID: "tasks[1]", Kind: "block", Selected: true,
					Block:  []planTask{{ID: "tasks[1].block[0]", Method: "noop", Selected: true}},
					Rescue: []planTask{{ID: "tasks[1].rescue[0]", Method: "noop", Selected: true}},
					Always: []planTask{{ID: "tasks[1].always[0]", Method: "noop", Selected: true}}},
				{ID: "tasks[2]", Kind: "parallel", Selected: true,
					Parallel: []planTask{{ID: "tasks[2].parallel[0]", Method: "noop", Selected: true}}},
				{ID: "tasks[3]", Method: "noop", Selected: false},
			}},
		},
		Tasks: []taskReport{{ID: "pretasks[0]", Status: statusOK}, {ID: "tasks[0]", Status: statusFailed}},
	}
	addNotRun(rep, notRunAborted)
	want := []string{"pretasks[0]", "tasks[0]", "tasks[1].block[0]", "tasks[1].rescue[0]", "tasks[1].always[0]", "tasks[2].parallel[0]"}
	if len(rep.Tasks) != len(want) {
		t.Fatalf("tasks = %+v, want %v", rep.Tasks, want)
	}
	for i, id := range want {
		got := rep.Tasks[i]
		if got.ID != id {
			t.Errorf("tasks[%d] = %s, want %s", i, got.ID, id)
		}
		if i >= 2 && (got.Status != statusNotRun || got.Reason != notRunAborted || got.Method != "noop") {
			t.Errorf("%s = %+v, want not_run for an abort", id, got)
		}
	}
}

// TestStoppedReason names each failure and hold no rescue answered, by its
// device's name, and only those.
func TestStoppedReason(t *testing.T) {
	for name, c := range map[string]struct {
		tasks []taskReport
		want  string
	}{
		"a failure": {[]taskReport{{ID: "tasks[0]", Status: statusOK}, {ID: "tasks[1]", Device: "id-1", Host: "r1", Status: statusFailed}},
			"the run stopped after tasks[1] [r1] failed"},
		"a hold and a failure": {[]taskReport{{ID: "tasks[0]", Status: statusHeld}, {ID: "tasks[1]", Device: "id-2", Status: statusFailed}},
			"the run stopped after tasks[0] held, tasks[1] [id-2] failed"},
		"a rescued failure only": {[]taskReport{{ID: "tasks[0].block[0]", Status: statusFailed, RescuedBy: "tasks[0].rescue[0]"}}, notRunEnded},
		"nothing failed":         {[]taskReport{{ID: "tasks[0]", Status: statusOK}}, notRunEnded},
	} {
		if got := stoppedReason(c.tasks); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
