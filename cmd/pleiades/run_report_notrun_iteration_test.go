// Tests that a task that ran more than once is not reported as never run, and
// that a retried or ignored failure is not named as what ended the run.
package main

import "testing"

func TestAddNotRun_ATaskThatRanAsTriesIsNotListed(t *testing.T) {
	rep := &runReport{
		Plan:  []planSection{{Tasks: []planTask{{ID: "tasks[0]", Selected: true}, {ID: "tasks[1]", Selected: true}}}},
		Tasks: []taskReport{{ID: "tasks[0].attempt[0]", Status: statusFailed, Retried: true}, {ID: "tasks[0].attempt[1]", Status: statusOK}},
	}
	addNotRun(rep, stoppedReason(rep.Tasks))
	if len(rep.Tasks) != 3 || rep.Tasks[2].ID != "tasks[1]" {
		t.Fatalf("tasks %+v; want only tasks[1] listed as not run", rep.Tasks)
	}
	if rep.Tasks[2].Reason != notRunEnded {
		t.Errorf("reason %q names a retried failure as what ended the run", rep.Tasks[2].Reason)
	}
}
