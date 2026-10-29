// The sampler that follows a run: it reads the machine every SampleEvery,
// keeps the least memory seen and the most swap grown, charges each package
// running at the time with the fall it saw, and asks for the run to be
// stopped the first time a reading says so (Stop). Its decisions are plain
// functions of the readings, so a test feeds it readings rather than a
// shortage.
package headroom

import (
	"sync"
	"time"
)

// Watch is one run's sampler.
type Watch struct {
	read  func() (Memory, error)
	stop  func(reason string)
	every time.Duration
	done  chan struct{}
	ended chan struct{}

	mu      sync.Mutex
	start   Memory
	min     float64
	swap    float64
	running map[string]bool
	peak    map[string]float64
	stopped string
	samples int
}

// Report is what a Watch saw over a run.
type Report struct {
	// Start is the reading as the run began.
	Start Memory
	// MinAvailableMB is the least memory available seen.
	MinAvailableMB float64
	// SwapGrowthMB is the most the swap in use grew past Start's.
	SwapGrowthMB float64
	// PeakMB is the largest Fall seen while each package ran, by package. With
	// several running at once each is charged the whole fall, so it is a bound
	// on what a package took, not its share; with one at a time it is its own.
	PeakMB map[string]float64
	// Stopped is why the watch asked for the run to be stopped, or "".
	Stopped string
	// Samples is how many readings were taken.
	Samples int
}

// StartWatch begins sampling with read every interval, from the reading
// start, calling stop once with the reason the first time a reading says
// the run must stop. Finish ends it.
func StartWatch(start Memory, read func() (Memory, error), every time.Duration, stop func(reason string)) *Watch {
	w := &Watch{read: read, stop: stop, every: every, done: make(chan struct{}), ended: make(chan struct{}),
		start: start, min: start.AvailableMB, running: map[string]bool{}, peak: map[string]float64{}}
	go w.loop()
	return w
}

// loop samples until Finish.
func (w *Watch) loop() {
	defer close(w.ended)
	ticker := time.NewTicker(w.every)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			m, err := w.read()
			if err != nil {
				continue
			}
			if reason := w.Sample(m); reason != "" {
				w.stop(reason)
			}
		}
	}
}

// Sample folds one reading in and returns the reason the run must stop,
// the first time one applies, or "". It is what loop calls with each
// reading, exported so a test can feed readings without a clock.
func (w *Watch) Sample(m Memory) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.samples++
	w.min = min(w.min, m.AvailableMB)
	fall := Fall(m, w.start)
	w.swap = max(w.swap, m.SwapUsedMB-w.start.SwapUsedMB)
	for pkg := range w.running {
		w.peak[pkg] = max(w.peak[pkg], fall)
	}
	if w.stopped != "" {
		return ""
	}
	if reason := Stop(m, w.start); reason != "" {
		w.stopped = reason
		return reason
	}
	return ""
}

// Began notes that pkg's tests are running, so readings from now are
// charged to it.
func (w *Watch) Began(pkg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running[pkg] = true
	if _, ok := w.peak[pkg]; !ok {
		w.peak[pkg] = 0
	}
}

// Ended notes that pkg's tests have finished.
func (w *Watch) Ended(pkg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.running, pkg)
}

// Stopped is why the watch asked for the run to be stopped, or "".
func (w *Watch) Stopped() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped
}

// Peaks is PeakMB so far, a copy.
func (w *Watch) Peaks() map[string]float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.peaksLocked()
}

// peaksLocked is Peaks with the lock held.
func (w *Watch) peaksLocked() map[string]float64 {
	out := make(map[string]float64, len(w.peak))
	for k, v := range w.peak {
		out[k] = v
	}
	return out
}

// Finish ends the sampling and returns what the watch saw. It may be called
// more than once; sampling ends on the first call.
func (w *Watch) Finish() Report {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	<-w.ended
	w.mu.Lock()
	defer w.mu.Unlock()
	return Report{Start: w.start, MinAvailableMB: w.min, SwapGrowthMB: w.swap, PeakMB: w.peaksLocked(), Stopped: w.stopped, Samples: w.samples}
}
