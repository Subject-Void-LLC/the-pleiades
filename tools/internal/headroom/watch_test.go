// Tests for the sampler: its account of the readings it is fed, and the one
// stop it asks for.
package headroom

import (
	"sync"
	"testing"
	"time"
)

// TestWatch_ChargesTheRunningPackages: each reading's fall below the start
// is charged to every package running then, the least memory and the most
// swap growth are kept, and a package that began is in the account even
// when nothing was read while it ran.
func TestWatch_ChargesTheRunningPackages(t *testing.T) {
	start := Memory{TotalMB: 8000, AvailableMB: 4000, SwapUsedMB: 100}
	w := StartWatch(start, func() (Memory, error) { return start, nil }, time.Hour, func(string) { t.Error("stopped") })
	w.Began("a")
	w.Sample(Memory{TotalMB: 8000, AvailableMB: 3500, SwapUsedMB: 100})
	w.Began("b")
	w.Sample(Memory{TotalMB: 8000, AvailableMB: 3000, SwapUsedMB: 300})
	w.Ended("a")
	w.Sample(Memory{TotalMB: 8000, AvailableMB: 3800, SwapUsedMB: 300})
	w.Began("c")
	w.Ended("c")
	r := w.Finish()
	if r.PeakMB["a"] != 1200 || r.PeakMB["b"] != 1200 || r.PeakMB["c"] != 0 {
		t.Errorf("peaks = %v, want a and b charged 1200 (1000 of memory and 200 of swap) and c 0", r.PeakMB)
	}
	if r.MinAvailableMB != 3000 || r.SwapGrowthMB != 200 || r.Samples != 3 || r.Stopped != "" || r.Start != start {
		t.Errorf("report = %+v", r)
	}
	// Finish again returns the same account and does not block.
	if again := w.Finish(); again.Samples != 3 {
		t.Errorf("a second Finish = %+v", again)
	}
}

// TestWatch_StopsOnce: the first reading under the floor asks for the stop,
// with its reason, and no later reading asks again.
func TestWatch_StopsOnce(t *testing.T) {
	start := Memory{TotalMB: 8000, AvailableMB: 4000}
	var stops []string
	w := StartWatch(start, func() (Memory, error) { return start, nil }, time.Hour, func(reason string) { stops = append(stops, reason) })
	if got := w.Sample(Memory{TotalMB: 8000, AvailableMB: 650}); got == "" {
		t.Fatal("a reading under the floor asked for no stop")
	}
	if got := w.Sample(Memory{TotalMB: 8000, AvailableMB: 600}); got != "" {
		t.Errorf("a second reading asked again: %q", got)
	}
	r := w.Finish()
	if r.Stopped != "memory available 650 MB, under the 700 MB floor" || len(stops) != 0 {
		t.Errorf("stopped = %q, stop calls from Sample = %v (loop calls stop, Sample only decides)", r.Stopped, stops)
	}
}

// TestWatch_LoopReadsAndStops: the loop reads on its interval, skips a
// reading that fails, and calls stop once when a reading says so.
func TestWatch_LoopReadsAndStops(t *testing.T) {
	start := Memory{TotalMB: 8000, AvailableMB: 4000, SwapUsedMB: 0}
	var mu sync.Mutex
	readings := []Memory{{TotalMB: 8000, AvailableMB: 3000}, {}, {TotalMB: 8000, AvailableMB: 3000, SwapUsedMB: SwapGrowthMB}, {TotalMB: 8000, AvailableMB: 100}}
	i := 0
	read := func() (Memory, error) {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(readings) {
			return readings[len(readings)-1], nil
		}
		m := readings[i]
		i++
		if m == (Memory{}) {
			return Memory{}, errorf("the reader failed this once")
		}
		return m, nil
	}
	stopped := make(chan string, 4)
	w := StartWatch(start, read, 5*time.Millisecond, func(reason string) { stopped <- reason })
	select {
	case reason := <-stopped:
		if reason != "swap in use grew 3072 MB since the run began, 3072 MB allowed" {
			t.Errorf("stopped for %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never asked for a stop")
	}
	r := w.Finish()
	// No count of later samples is asserted: how many ticks land between the
	// stop and Finish is the scheduler's, not the test's (FAILURE_PATTERNS
	// 687's class). The readings before the stop were sampled, the failed
	// one was not, and the stop was asked for once.
	if len(stopped) != 0 {
		t.Errorf("stop was called %d more time(s)", len(stopped))
	}
	if r.Samples < 2 || r.MinAvailableMB > 3000 || r.Stopped == "" {
		t.Errorf("report = %+v: the two readings before the stop are samples", r)
	}
}

// errorf is a small error for the reader stand-in.
type errorf string

func (e errorf) Error() string { return string(e) }
