// Package engine: the timed pause action, a hold of seconds or minutes in a
// run, most often between serial: batches, as Ansible's ansible.builtin.pause.
// It is not pause.go's pause-for-a-decision (Phase 27), which waits for a
// person and is not built.
package engine

import (
	"context"
	"fmt"
	"time"
)

// The names of the pause action: the bare one a runbook writes and the
// namespaced one, both dispatching identically.
const (
	PauseName = "pause"
	PauseFQCN = "pleiades.builtin.pause"
)

// MaxPause is the longest hold one pause may ask for: a day, the same bound
// a task's own timeout has.
const MaxPause = 24 * time.Hour

// isPause reports whether fqcn names the pause action.
func isPause(fqcn string) bool { return fqcn == PauseName || fqcn == PauseFQCN }

// PauseDuration reads a pause's params: exactly one of seconds or minutes, a
// whole number from 1 up to MaxPause. A prompt, or a pause with no duration,
// waits for a person, which an unattended run (a schedule, a Controller's
// job) cannot answer, so it is refused rather than left to block.
func PauseDuration(params map[string]any) (time.Duration, error) {
	for key := range params {
		switch key {
		case "seconds", "minutes":
		case "prompt", "echo":
			return 0, fmt.Errorf("pause: %s waits for a person to answer, which a run does not do; give seconds or minutes", key)
		default:
			return 0, fmt.Errorf("pause: %q is not a parameter; pause takes seconds or minutes", key)
		}
	}
	_, hasSeconds := params["seconds"]
	_, hasMinutes := params["minutes"]
	switch {
	case hasSeconds && hasMinutes:
		return 0, fmt.Errorf("pause: give seconds or minutes, not both")
	case !hasSeconds && !hasMinutes:
		return 0, fmt.Errorf("pause: a pause with no seconds or minutes waits for a person, which a run does not do")
	}
	key, unit := "seconds", time.Second
	if hasMinutes {
		key, unit = "minutes", time.Minute
	}
	n, ok := wholeNumber(params[key])
	if !ok || n < 1 {
		return 0, fmt.Errorf("pause: %s must be a whole number of at least 1, not %v", key, params[key])
	}
	if time.Duration(n) > MaxPause/unit {
		return 0, fmt.Errorf("pause: %d %s is longer than the most a pause may hold, %s", n, key, MaxPause)
	}
	return time.Duration(n) * unit, nil
}

// wholeNumber reads v as a whole number, from the shapes YAML and JSON decode
// a number into.
func wholeNumber(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
	}
	return 0, false
}

// runPause holds for the task's duration, or until ctx ends, and changes
// nothing. A check holds for nothing: it says how long the run would.
func runPause(ctx context.Context, task *Task, check bool) (ActionResult, error) {
	d, err := PauseDuration(task.Params)
	if err != nil {
		return ActionResult{}, err
	}
	if check {
		return ActionResult{}, nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ActionResult{}, nil
	case <-ctx.Done():
		return ActionResult{}, fmt.Errorf("pause: stopped after less than %s: %w", d, ctx.Err())
	}
}
