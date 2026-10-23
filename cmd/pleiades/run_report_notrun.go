// The tasks a run planned and never reached. A refused or stopped run's
// report lists each of them with status not_run and the reason, so a reader
// asking "did the apply run?" finds the answer in tasks rather than having
// to compare the plan against what ran.
package main

import (
	"strings"
)

// The reasons a planned task was never run.
const (
	// notRunRefused is the reason when validation refused the runbook.
	notRunRefused = "validation refused the runbook, so nothing ran"
	// notRunNotStarted is the reason when the run stopped before its
	// first task, after validation passed (the project's runs were busy,
	// or its journal could not be opened).
	notRunNotStarted = "the run stopped before any task ran"
	// notRunAborted is the reason when the executor aborted the run.
	notRunAborted = "the run was aborted before reaching this task"
	// notRunEnded is the reason when a run ended without a failure that
	// explains it, which the engine does not do; it is here so such a run
	// still names what it left out rather than dropping it.
	notRunEnded = "the run ended before reaching this task"
)

// addNotRun appends a not_run entry, with reason, for every selected task
// in rep's plan that has no result in rep.Tasks, in the plan's order. A
// group (block or parallel) is not a task of its own, so only its tasks
// are listed; a task --tags left out was never planned, and is not listed.
func (rep *runReport) addNotRun(reason string) {
	ran := make(map[string]bool, len(rep.Tasks))
	for _, t := range rep.Tasks {
		ran[t.ID] = true
	}
	var walk func(tasks []planTask)
	walk = func(tasks []planTask) {
		for _, task := range tasks {
			// A group's own tasks are walked in the order the plan prints
			// them: block, rescue and always, or parallel.
			if task.Kind != "" {
				walk(task.Block)
				walk(task.Rescue)
				walk(task.Always)
				walk(task.Parallel)
				continue
			}
			if !task.Selected || ran[task.ID] {
				continue
			}
			rep.Tasks = append(rep.Tasks, taskReport{ID: task.ID, Name: task.Name, Method: task.Method, Status: statusNotRun, Reason: reason})
		}
	}
	for _, section := range rep.Plan {
		walk(section.Tasks)
	}
}

// stoppedReason is why a finished run left tasks unreached: the failed and
// held tasks no rescue answered, each with its device's name, since one of
// them is what ended the run.
func stoppedReason(tasks []taskReport) string {
	var stopped []string
	for _, t := range tasks {
		if (t.Status == statusFailed || t.Status == statusHeld) && t.RescuedBy == "" {
			stopped = append(stopped, taskLabel(t)+" "+string(t.Status))
		}
	}
	if len(stopped) == 0 {
		return notRunEnded
	}
	return "the run stopped after " + strings.Join(stopped, ", ")
}
