// Latency measurement shared by the stress gate and the pywinrm
// comparison, so the two report the same statistics computed the same
// way and a comparison between them means something.
package winrmexec

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// latencies is one workload's per-call durations, sorted ascending.
type latencies []time.Duration

// newLatencies returns d sorted, as latencies.
func newLatencies(d []time.Duration) latencies {
	sorted := append(latencies(nil), d...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	return sorted
}

// String reports the count, median, 95th percentile and maximum, each
// rounded to the millisecond, or says there is nothing to report.
func (l latencies) String() string {
	if len(l) == 0 {
		return "n=0"
	}
	return fmt.Sprintf("n=%d  p50=%v  p95=%v  max=%v", len(l),
		l[len(l)/2].Round(time.Millisecond), l[len(l)*95/100].Round(time.Millisecond), l[len(l)-1].Round(time.Millisecond))
}

// measureExecute runs cmd n times through Execute against target,
// workers at a time, and returns every call's duration. Every call must
// succeed: a failure or a non-zero exit fails t, because a latency
// measured over failed commands is not the latency of the work.
func measureExecute(t *testing.T, target Target, auth Auth, opts Options, n, workers int, cmd Command) latencies {
	t.Helper()
	durations := make([]time.Duration, n)
	errs := make(chan error, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				start := time.Now()
				res, err := Execute(context.Background(), target, auth, cmd, opts)
				durations[i] = time.Since(start)
				if err == nil && res.ExitCode != 0 {
					err = fmt.Errorf("exit %d: %s", res.ExitCode, res.Stderr)
				}
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a command failed: %v", err)
	}
	return newLatencies(durations)
}

// TestLatencies_String pins the report's arithmetic on a known series.
func TestLatencies_String(t *testing.T) {
	var d []time.Duration
	for i := 100; i >= 1; i-- {
		d = append(d, time.Duration(i)*time.Millisecond)
	}
	if got, want := newLatencies(d).String(), "n=100  p50=51ms  p95=96ms  max=100ms"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got := newLatencies(nil).String(); got != "n=0" {
		t.Errorf("empty: String() = %q, want n=0", got)
	}
}
