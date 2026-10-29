// The sampler that follows a run's disk: it reads the space free on each
// file system the run writes to every SampleEvery, keeps the least seen,
// and charges each package running at the time with the fall it saw, so a
// run's record says what each package wrote (its build cache, its test
// binaries, its temporary files) as well as what it took in memory.
package headroom

import (
	"sync"
	"time"
)

// DiskWatch is one run's disk sampler.
type DiskWatch struct {
	read  func(string) (float64, error)
	paths []string
	every time.Duration
	done  chan struct{}
	ended chan struct{}

	mu      sync.Mutex
	start   map[string]float64
	min     map[string]float64
	running map[string]bool
	peak    map[string]float64
	samples int
}

// DiskReport is what a DiskWatch saw over a run.
type DiskReport struct {
	// StartFreeGB and MinFreeGB are the space free on each path's file
	// system as the run began and at the least, by path.
	StartFreeGB, MinFreeGB map[string]float64
	// PeakMB is the most each package's run cost the disk, in MB: the largest
	// fall in free space below the start on any one file system while it
	// ran (the largest, not the sum, since two paths are often one file
	// system). With several packages running at once each is charged the
	// whole fall, so it is a bound; with one at a time it is its own.
	PeakMB map[string]float64
	// Samples is how many readings were taken.
	Samples int
}

// StartDiskWatch begins sampling the space free under each of paths (the
// readings at start are the run's baseline) with read every interval, until
// Finish. Paths whose readings fail are left out of the report.
func StartDiskWatch(paths []string, start map[string]float64, read func(string) (float64, error), every time.Duration) *DiskWatch {
	w := &DiskWatch{read: read, paths: paths, every: every, done: make(chan struct{}), ended: make(chan struct{}),
		start: map[string]float64{}, min: map[string]float64{}, running: map[string]bool{}, peak: map[string]float64{}}
	for _, p := range paths {
		if gb, ok := start[p]; ok {
			w.start[p], w.min[p] = gb, gb
		}
	}
	go w.loop()
	return w
}

// loop samples until Finish.
func (w *DiskWatch) loop() {
	defer close(w.ended)
	ticker := time.NewTicker(w.every)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			free := map[string]float64{}
			for _, p := range w.paths {
				if gb, err := w.read(p); err == nil {
					free[p] = gb
				}
			}
			w.Sample(free)
		}
	}
}

// Sample folds one reading in: the space free on each path, in GB. It is
// what loop calls, exported so a test can feed readings without a clock.
func (w *DiskWatch) Sample(freeGB map[string]float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.samples++
	fall := 0.0
	for p, gb := range freeGB {
		if start, ok := w.start[p]; ok {
			w.min[p] = min(w.min[p], gb)
			fall = max(fall, (start-gb)*1024)
		}
	}
	for pkg := range w.running {
		w.peak[pkg] = max(w.peak[pkg], fall)
	}
}

// Began notes that pkg's run began, so readings from now are charged to it.
func (w *DiskWatch) Began(pkg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running[pkg] = true
	if _, ok := w.peak[pkg]; !ok {
		w.peak[pkg] = 0
	}
}

// Ended notes that pkg's run has finished.
func (w *DiskWatch) Ended(pkg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.running, pkg)
}

// Peaks is PeakMB so far, a copy.
func (w *DiskWatch) Peaks() map[string]float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return copyMap(w.peak)
}

// Finish ends the sampling and returns what the watch saw. It may be called
// more than once; sampling ends on the first call.
func (w *DiskWatch) Finish() DiskReport {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	<-w.ended
	w.mu.Lock()
	defer w.mu.Unlock()
	return DiskReport{StartFreeGB: copyMap(w.start), MinFreeGB: copyMap(w.min), PeakMB: copyMap(w.peak), Samples: w.samples}
}

// copyMap is a copy of m.
func copyMap(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
