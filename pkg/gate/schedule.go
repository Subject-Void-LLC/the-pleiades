// Running a plan: bounded parallelism, heavy checks one at a time, serial
// checks alone, and a skip for every check the run never started.
package gate

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Runner runs one selected check and returns its result. It owns the
// check's own timeout (p.Timeout); ctx carries the run's deadline and
// cancellation, which it honors by stopping the check.
type Runner func(ctx context.Context, p Planned) Result

// Run runs plan's selected checks with run and returns one result per
// planned check, in plan's order.
//
// Checks start in plan's order. At most parallelism run at once (at least
// one), at most one heavy check runs at a time, and a serial check starts
// only once everything before it has finished and runs with nothing else.
// A check not selected is a SKIP with its plan's reason; one the run never
// started because ctx ended is a SKIP saying so.
func Run(ctx context.Context, plan []Planned, parallelism int, run Runner) []Result {
	parallelism = min(max(parallelism, 1), MaxParallelism)
	results := make([]Result, len(plan))
	slots := make(chan struct{}, parallelism)
	heavy := make(chan struct{}, 1)
	var running sync.WaitGroup

	for i, p := range plan {
		if !p.Selected {
			results[i] = Skipped(p.Check, p.Reason)
			continue
		}
		if p.Check.Serial {
			running.Wait()
			if ctx.Err() != nil {
				results[i] = notStarted(ctx, p.Check)
				continue
			}
			results[i] = runOne(ctx, p, run)
			continue
		}
		if !acquire(ctx, slots) {
			results[i] = notStarted(ctx, p.Check)
			continue
		}
		if p.Check.Heavy && !acquire(ctx, heavy) {
			<-slots
			results[i] = notStarted(ctx, p.Check)
			continue
		}
		running.Add(1)
		go func(i int, p Planned) {
			defer running.Done()
			defer func() { <-slots }()
			if p.Check.Heavy {
				defer func() { <-heavy }()
			}
			results[i] = runOne(ctx, p, run)
		}(i, p)
	}
	running.Wait()
	return results
}

// acquire takes a place in ch, or reports false when ctx ends first.
func acquire(ctx context.Context, ch chan struct{}) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case ch <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// runOne runs p and holds its result to the check's own name and kind; a
// runner that panics is a FAIL naming the panic, never a lost result.
func runOne(ctx context.Context, p Planned, run Runner) (r Result) {
	defer func() {
		if v := recover(); v != nil {
			r = Result{Status: Fail, Reason: fmt.Sprintf("the runner stopped unexpectedly: %v", v), ExitCode: NoExit}
		}
		r.Name, r.Kind = p.Check.Name, p.Check.Kind
		if r.Status != Pass && r.Status != Fail && r.Status != Skip {
			r.Status, r.Reason = Fail, fmt.Sprintf("the runner gave no outcome (%q)", r.Status)
		}
	}()
	return run(ctx, p)
}

// notStarted is the SKIP for a check the run never started because ctx
// ended.
func notStarted(ctx context.Context, c Check) Result {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Skipped(c, "the task's deadline passed before it started")
	}
	return Skipped(c, "the run was canceled before it started")
}
