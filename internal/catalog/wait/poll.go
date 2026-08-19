package wait

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The schedule both wait methods run on, and the recording they both do
// once the wait ends.
//
// It is separate from wait.go, which reads and checks the parameters,
// because the two answer different questions: that file decides what the
// task asked for, and this one decides when to look and what to say
// afterward. Both are shared by wait.path and wait.search, and the wait
// prefix on every name here is what keeps them from colliding with
// anything either method's own file declares, since sibling files share
// one Go package and Go has no file-level scope.

// waitPoll runs probe on req's schedule until it reports the condition
// holds, and returns how long that took.
//
// what describes the condition in the caller's own words ("for /tmp/x to
// exist"), and is the only part of the timeout and cancellation messages
// that differs between the two methods, so the two share one wording of
// the sentence around it.
//
// # A failing probe ends the run rather than being retried
//
// A probe that returns an error did not answer the question, and the
// reasons it fails (a dropped connection, a device refusing sessions,
// output nothing here can parse) are not conditions that come true if
// asked again. Retrying would spend the whole timeout on a device that
// already said no and then report a timeout, which names the wrong
// cause: an operator would go looking for the file rather than for the
// connection. Absence itself is never an error, since remotefile.Stat
// and remotefile.Read both report a missing path as an answer, so the
// case a wait actually cares about never reaches this branch.
func waitPoll(ctx context.Context, req waitRequest, what string, probe func(context.Context) (bool, error)) (time.Duration, error) {
	start := time.Now()
	deadline := start.Add(req.Timeout)

	// The delay before the first probe and the interval between later
	// ones are the same sleep, which is why this loop sleeps at the top
	// rather than at the bottom. Two separate sleeps would be two places
	// to forget the context check, and the context check is the whole
	// reason a torn-down run does not keep polling.
	interval := req.Delay
	for {
		if interval > 0 {
			if err := waitSleep(ctx, interval); err != nil {
				return 0, fmt.Errorf("waiting %s: %w", what, err)
			}
		}

		ok, err := probe(ctx)
		if err != nil {
			return 0, err
		}
		if ok {
			return time.Since(start), nil
		}

		// The deadline is checked after a probe rather than before one, so
		// the condition always gets at least one look. Checking first would
		// mean a task whose delay reached the deadline never observed the
		// device at all, and reported a timeout about something it never
		// looked at.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, fmt.Errorf("timed out after %s waiting %s", req.Timeout, what)
		}

		interval = req.Sleep
		// Capped so the last probe lands on the deadline instead of past
		// it. Without this a task with sleep longer than what is left
		// would sit idle well beyond the timeout it was given, and the
		// elapsed time reported for the failure would not be the timeout
		// the author wrote.
		if interval > remaining {
			interval = remaining
		}
	}
}

// waitSleep waits for d, or until ctx ends, whichever comes first.
//
// time.Sleep is what this deliberately is not. A run being torn down has
// to stop now, and a plain sleep would hold the device lock this task
// took for the rest of a five minute timeout while nothing was left to
// use the answer.
func waitSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	// Stopped rather than left to fire, so a long timeout's timer is not
	// held alive by the runtime after a cancelled wait returns.
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// waitRecord writes what both methods report: the diff, the path they
// watched and how long they waited.
//
// The diff goes through sdk.Unchanged, whose two halves are the same
// observation, and recording it at all is the point. A wait changed
// nothing, so there is no before and after to show, but a journal still
// has to be able to tell "this task observed the device and found this"
// from "this task was never recorded", and only a recorded diff says the
// first.
//
// It is called only on a run that succeeded. A run that timed out or was
// cancelled records nothing, because the engine discards a Result that
// arrives with an error and a stat left behind would describe an
// observation the task never got to make.
func waitRecord(rc sdk.RunbookContext, req waitRequest, state map[string]any, elapsed time.Duration) error {
	if err := sdk.RecordDiff(rc, sdk.Unchanged(state)); err != nil {
		return err
	}
	if err := rc.SetStat(waitParamPath, req.Path); err != nil {
		return err
	}
	return rc.SetStat(waitStatElapsed, int(elapsed.Seconds()))
}
