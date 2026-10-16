// Tests that a plan runs within its bounds: parallelism, one heavy check
// at a time, serial checks alone, and a skip for whatever never started.
package gate

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// concurrency records how many checks a runner saw running at once, and
// which ran alongside a serial or heavy one.
type concurrency struct {
	mu                  sync.Mutex
	now, peak, heavyNow int
	heavyPeak           int
	serialShared        bool
	serialRunning       bool
}

// runner returns a Runner that holds each check for d and records overlap.
func (c *concurrency) runner(d time.Duration) Runner {
	return func(ctx context.Context, p Planned) Result {
		c.mu.Lock()
		if c.serialRunning || (p.Check.Serial && c.now > 0) {
			c.serialShared = true
		}
		c.now++
		c.peak = max(c.peak, c.now)
		if p.Check.Heavy {
			c.heavyNow++
			c.heavyPeak = max(c.heavyPeak, c.heavyNow)
		}
		if p.Check.Serial {
			c.serialRunning = true
		}
		c.mu.Unlock()
		time.Sleep(d)
		c.mu.Lock()
		c.now--
		if p.Check.Heavy {
			c.heavyNow--
		}
		if p.Check.Serial {
			c.serialRunning = false
		}
		c.mu.Unlock()
		return Result{Status: Pass, ExitCode: 0}
	}
}

// planOf selects every check in checks.
func planOf(checks ...Check) []Planned {
	plan := make([]Planned, len(checks))
	for i, c := range checks {
		plan[i] = Planned{Check: c, Selected: true}
	}
	return plan
}

// TestRun_HoldsEveryBound runs light, heavy and serial checks together and
// finds parallelism, the one-heavy rule and the serial rule each held, with
// results in plan order.
func TestRun_HoldsEveryBound(t *testing.T) {
	// The heavy checks come first, so without the one-heavy rule both would
	// start at once rather than as earlier checks happen to finish.
	checks := []Check{{Name: "heavy1", Kind: KindDB, Heavy: true}, {Name: "heavy2", Kind: KindDB, Heavy: true}}
	for i := range 8 {
		checks = append(checks, Check{Name: "light" + string(rune('a'+i)), Kind: KindPure})
	}
	checks = append(checks, Check{Name: "serial", Kind: KindPure, Serial: true})
	checks = append(checks, Check{Name: "after", Kind: KindPure}, Check{Name: "heavy3", Kind: KindDB, Heavy: true})
	var c concurrency
	results := Run(context.Background(), planOf(checks...), 3, c.runner(20*time.Millisecond))

	if c.peak > 3 || c.peak < 2 {
		t.Errorf("peak concurrency %d, want 2 or 3 under parallelism 3", c.peak)
	}
	if c.heavyPeak != 1 {
		t.Errorf("%d heavy checks ran at once, want one", c.heavyPeak)
	}
	if c.serialShared {
		t.Error("a serial check ran beside another check")
	}
	for i, r := range results {
		if r.Name != checks[i].Name || r.Status != Pass {
			t.Errorf("results[%d] = %+v, want %s passed", i, r, checks[i].Name)
		}
	}
}

// TestRun_ParallelismOneRunsOneAtATime is the default: serial unless a
// manifest or task raises it.
func TestRun_ParallelismOneRunsOneAtATime(t *testing.T) {
	var c concurrency
	Run(context.Background(), planOf(Check{Name: "a"}, Check{Name: "b"}, Check{Name: "c"}), 0, c.runner(5*time.Millisecond))
	if c.peak != 1 {
		t.Errorf("peak concurrency %d with parallelism 0, want 1", c.peak)
	}
}

// TestRun_ASkipStaysASkip: an unselected check is a SKIP with its plan's
// reason, and a check the run never started because the deadline passed,
// or the run was canceled, is a SKIP saying which.
func TestRun_ASkipStaysASkip(t *testing.T) {
	plan := planOf(Check{Name: "ran"}, Check{Name: "late"})
	plan = append(plan, Planned{Check: Check{Name: "unselected"}, Reason: "its kind is not selected"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	results := Run(ctx, plan, 1, func(ctx context.Context, p Planned) Result {
		<-ctx.Done()
		return Result{Status: Fail, Reason: "stopped by the deadline", ExitCode: NoExit}
	})
	if results[0].Status != Fail {
		t.Errorf("the running check = %+v, want the runner's FAIL", results[0])
	}
	if results[1].Status != Skip || !strings.Contains(results[1].Reason, "deadline passed before it started") {
		t.Errorf("the never-started check = %+v", results[1])
	}
	if results[2].Status != Skip || results[2].Reason != "its kind is not selected" {
		t.Errorf("the unselected check = %+v", results[2])
	}

	canceled, stop := context.WithCancel(context.Background())
	stop()
	got := Run(canceled, planOf(Check{Name: "a", Serial: true}, Check{Name: "b"}), 2, func(context.Context, Planned) Result {
		t.Error("a check ran after the run was canceled")
		return Result{Status: Pass}
	})
	for _, r := range got {
		if r.Status != Skip || !strings.Contains(r.Reason, "canceled before it started") {
			t.Errorf("result = %+v, want a canceled SKIP", r)
		}
	}
}

// TestRun_ARunnerThatPanicsOrSaysNothingFails: neither is ever a lost or
// passing result.
func TestRun_ARunnerThatPanicsOrSaysNothingFails(t *testing.T) {
	var calls atomic.Int32
	results := Run(context.Background(), planOf(Check{Name: "panics", Kind: KindPure}, Check{Name: "silent", Kind: KindPure}), 1, func(_ context.Context, p Planned) Result {
		calls.Add(1)
		if p.Check.Name == "panics" {
			panic("broken runner")
		}
		return Result{}
	})
	if results[0].Status != Fail || !strings.Contains(results[0].Reason, "broken runner") || results[0].Name != "panics" {
		t.Errorf("panic = %+v", results[0])
	}
	if results[1].Status != Fail || results[1].Kind != KindPure {
		t.Errorf("no outcome = %+v", results[1])
	}
	if calls.Load() != 2 {
		t.Errorf("runner called %d times", calls.Load())
	}
}
