// Tests for the liveness heartbeat: the writer (heartbeat.go) and the
// freshness rule (heartbeat_check.go).
//
// The case that carries the weight is "a heartbeat stops the moment its
// consumer stops answering". Everything else here is a guard around it.
// These run against a fake ConsumerProbe because the point is to control
// exactly when the evidence disappears; the same claim against a real
// NATS container, with a real severed connection and the real binary, is
// the release gate in tests/e2e/runner_heartbeat_release_gate_test.go.
package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// fakeConsumerProbe answers Info with whatever error the test currently
// wants, and counts the calls.
//
// A mutex rather than an atomic bool because Run calls Info from its own
// goroutine while the test flips the answer, and the -race detector is
// the point of the exercise, not an obstacle to it.
type fakeConsumerProbe struct {
	mu    sync.Mutex
	err   error
	calls int
}

// Info implements ConsumerProbe.
func (f *fakeConsumerProbe) Info(ctx context.Context) (*jetstream.ConsumerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &jetstream.ConsumerInfo{Name: "fake"}, nil
}

// fail makes every later Info answer with err, which is what a severed
// connection looks like from inside a beat.
func (f *fakeConsumerProbe) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// callCount reports how many times Info has been asked.
func (f *fakeConsumerProbe) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newTestHeartbeat builds a Heartbeat under a temporary directory.
func newTestHeartbeat(t *testing.T, interval time.Duration, probe ConsumerProbe) *Heartbeat {
	t.Helper()
	path := HeartbeatPath(filepath.Join(t.TempDir(), "beats", "heartbeat"))
	hb, err := NewHeartbeat(path, interval, probe, nil)
	if err != nil {
		t.Fatalf("NewHeartbeat: %v", err)
	}
	return hb
}

func TestNewHeartbeatRefusesAnUnusableConfiguration(t *testing.T) {
	t.Run("no path", func(t *testing.T) {
		if _, err := NewHeartbeat("", time.Second, &fakeConsumerProbe{}, nil); err == nil {
			t.Fatal("accepted an empty path")
		}
	})

	t.Run("no probe", func(t *testing.T) {
		// THE CASE THIS GUARD EXISTS FOR. A Heartbeat with nothing to
		// probe is a timer, and a timer keeps beating through exactly the
		// failure this type was written to catch.
		path := HeartbeatPath(filepath.Join(t.TempDir(), "heartbeat"))
		if _, err := NewHeartbeat(path, time.Second, nil, nil); err == nil {
			t.Fatal("accepted a heartbeat with no consumer to probe, which cannot fail")
		}
	})

	t.Run("an unwritable directory fails at construction", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root, which can write into a 0500 directory, so this case cannot arise")
		}
		dir := t.TempDir()
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o500); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		path := HeartbeatPath(filepath.Join(locked, "heartbeat"))
		if _, err := NewHeartbeat(path, time.Second, &fakeConsumerProbe{}, nil); err == nil {
			t.Fatal("accepted a path it cannot write, which would only surface on the first tick")
		}
	})
}

func TestNewHeartbeatClearsAFileLeftByAPreviousProcess(t *testing.T) {
	// A Kubernetes emptyDir survives a container restart within the same
	// pod, so a Runner killed BY its liveness probe starts up next to the
	// heartbeat its previous life wrote. Leaving that file would let the
	// new process report healthy before it had reached the broker once.
	dir := t.TempDir()
	path := HeartbeatPath(filepath.Join(dir, "heartbeat"))
	if err := os.WriteFile(path.String(), []byte("a previous life\n"), 0o600); err != nil {
		t.Fatalf("seeding the stale heartbeat: %v", err)
	}

	if _, err := NewHeartbeat(path, time.Second, &fakeConsumerProbe{}, nil); err != nil {
		t.Fatalf("NewHeartbeat: %v", err)
	}

	if _, err := os.Stat(path.String()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale heartbeat survived construction (stat error %v); a restarted runner would report healthy before reaching the broker", err)
	}
}

func TestABeatIsWrittenOnlyWhenTheConsumerAnswers(t *testing.T) {
	probe := &fakeConsumerProbe{}
	hb := newTestHeartbeat(t, time.Second, probe)
	ctx := context.Background()

	if err := hb.beat(ctx); err != nil {
		t.Fatalf("beat with a healthy consumer: %v", err)
	}
	first, err := os.Stat(hb.Path().String())
	if err != nil {
		t.Fatalf("the healthy beat wrote no file: %v", err)
	}

	// THE ASSERTION FAILURE_PATTERNS.md #119 HAS BEEN WAITING FOR, in its
	// smallest form: the connection is gone, the process is fine, and the
	// file must not move.
	probe.fail(errors.New("nats: connection closed"))
	if err := hb.beat(ctx); err == nil {
		t.Fatal("beat reported success while the consumer was unreachable")
	}
	second, err := os.Stat(hb.Path().String())
	if err != nil {
		t.Fatalf("stat after the failed beat: %v", err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Fatalf("the heartbeat advanced from %s to %s while the consumer was unreachable, so this probe cannot fail",
			first.ModTime(), second.ModTime())
	}
}

func TestRunStopsBeatingWhenTheConsumerStopsAnswering(t *testing.T) {
	// The same claim as above through the real loop rather than through
	// one call, so the ticker cannot be the thing keeping the file fresh.
	probe := &fakeConsumerProbe{}
	hb := newTestHeartbeat(t, 10*time.Millisecond, probe)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		hb.Run(ctx)
	}()

	waitForHeartbeat(t, hb.Path())

	probe.fail(errors.New("nats: connection closed"))

	// Let several intervals pass. Every one of them ticks and every one
	// of them must withhold.
	callsAtCut := probe.callCount()
	time.Sleep(120 * time.Millisecond)
	frozen, err := os.Stat(hb.Path().String())
	if err != nil {
		t.Fatalf("stat after the cut: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	later, err := os.Stat(hb.Path().String())
	if err != nil {
		t.Fatalf("second stat after the cut: %v", err)
	}
	if !later.ModTime().Equal(frozen.ModTime()) {
		t.Fatalf("the heartbeat kept advancing (%s then %s) after the consumer stopped answering",
			frozen.ModTime(), later.ModTime())
	}

	// The loop has to still be TRYING. A heartbeat that stopped ticking
	// would freeze the file for the wrong reason and would look identical
	// from the outside.
	if probe.callCount() <= callsAtCut {
		t.Fatalf("the heartbeat stopped probing entirely (%d calls at the cut, %d now); it must keep asking so it recovers when the broker does",
			callsAtCut, probe.callCount())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was canceled")
	}
}

func TestRunResumesWhenTheConsumerAnswersAgain(t *testing.T) {
	// The other half of the contract: this is a probe, not a latch. A
	// broker restart must let the Runner report healthy again without a
	// restart of its own.
	probe := &fakeConsumerProbe{err: errors.New("nats: no servers available")}
	hb := newTestHeartbeat(t, 10*time.Millisecond, probe)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hb.Run(ctx)

	time.Sleep(60 * time.Millisecond)
	if _, err := os.Stat(hb.Path().String()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a heartbeat exists before the consumer ever answered (stat error %v)", err)
	}

	probe.fail(nil)
	waitForHeartbeat(t, hb.Path())
}

// waitForHeartbeat blocks until the heartbeat file exists, or fails the
// test. A poll rather than a fixed sleep, so the test is neither slower
// than it has to be nor flaky under load.
func waitForHeartbeat(t *testing.T, path HeartbeatPath) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path.String()); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no heartbeat was written at %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCheckHeartbeat(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	t.Run("a fresh heartbeat passes", func(t *testing.T) {
		path := writeHeartbeatAt(t, now.Add(-5*time.Second))
		if err := CheckHeartbeat(path, 30*time.Second, now); err != nil {
			t.Fatalf("CheckHeartbeat: %v", err)
		}
	})

	t.Run("a stale heartbeat reports its age and limit", func(t *testing.T) {
		path := writeHeartbeatAt(t, now.Add(-90*time.Second))
		err := CheckHeartbeat(path, 30*time.Second, now)
		var stale *StaleHeartbeatError
		if !errors.As(err, &stale) {
			t.Fatalf("CheckHeartbeat returned %v, want a StaleHeartbeatError", err)
		}
		if stale.Age != 90*time.Second || stale.MaxAge != 30*time.Second {
			t.Fatalf("StaleHeartbeatError carries age %s limit %s, want 1m30s and 30s", stale.Age, stale.MaxAge)
		}
	})

	t.Run("exactly at the limit is not yet stale", func(t *testing.T) {
		path := writeHeartbeatAt(t, now.Add(-30*time.Second))
		if err := CheckHeartbeat(path, 30*time.Second, now); err != nil {
			t.Fatalf("a heartbeat exactly at the limit was refused: %v", err)
		}
	})

	t.Run("a missing heartbeat is its own error", func(t *testing.T) {
		// The cold-start window. It has to be distinguishable from a
		// misconfigured path, because the caller answers them with
		// different exit codes.
		path := HeartbeatPath(filepath.Join(t.TempDir(), "never-written"))
		err := CheckHeartbeat(path, 30*time.Second, now)
		if !errors.Is(err, ErrHeartbeatMissing) {
			t.Fatalf("CheckHeartbeat returned %v, want ErrHeartbeatMissing", err)
		}
	})

	t.Run("a heartbeat from the future is not staleness", func(t *testing.T) {
		// A clock that stepped backwards is a problem to investigate and
		// not one a pod restart fixes.
		path := writeHeartbeatAt(t, now.Add(time.Hour))
		if err := CheckHeartbeat(path, 30*time.Second, now); err != nil {
			t.Fatalf("a future-dated heartbeat was reported stale: %v", err)
		}
	})

	t.Run("no path is refused", func(t *testing.T) {
		if err := CheckHeartbeat("", 30*time.Second, now); err == nil {
			t.Fatal("accepted an empty path")
		}
	})

	t.Run("a non-positive limit is refused", func(t *testing.T) {
		// Otherwise every runner in the fleet reports unhealthy at once
		// and the reason is a flag nobody looks at.
		path := writeHeartbeatAt(t, now)
		if err := CheckHeartbeat(path, 0, now); err == nil {
			t.Fatal("accepted a zero staleness limit")
		}
	})
}

func TestHeartbeatAge(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	path := writeHeartbeatAt(t, now.Add(-12*time.Second))
	age, err := HeartbeatAge(path, now)
	if err != nil {
		t.Fatalf("HeartbeatAge: %v", err)
	}
	if age != 12*time.Second {
		t.Fatalf("HeartbeatAge = %s, want 12s", age)
	}

	missing := HeartbeatPath(filepath.Join(t.TempDir(), "never-written"))
	if _, err := HeartbeatAge(missing, now); !errors.Is(err, ErrHeartbeatMissing) {
		t.Fatalf("HeartbeatAge on a missing file returned %v, want ErrHeartbeatMissing", err)
	}
}

// writeHeartbeatAt writes a heartbeat file whose modification time is
// exactly stamp, which is the property CheckHeartbeat reads.
func writeHeartbeatAt(t *testing.T, stamp time.Time) HeartbeatPath {
	t.Helper()
	path := HeartbeatPath(filepath.Join(t.TempDir(), "heartbeat"))
	if err := os.WriteFile(path.String(), []byte(stamp.Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		t.Fatalf("writing the heartbeat: %v", err)
	}
	if err := os.Chtimes(path.String(), stamp, stamp); err != nil {
		t.Fatalf("stamping the heartbeat: %v", err)
	}
	return path
}
