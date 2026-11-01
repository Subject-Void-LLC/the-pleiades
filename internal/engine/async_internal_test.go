// Internal tests of the async plumbing the run-level tests reach only in
// part: the poll interval, claiming a job, the leases a background job holds
// and shares, and their keep-alive, plus the small helpers a batched run uses
// (Limit.IsZero, RunResult.Absorb).
package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingLease is a lock.Lease that counts its renewals and releases.
type countingLease struct {
	mu        sync.Mutex
	id        string
	renewals  int
	releases  int
	renewedIn chan struct{}
}

// ID implements lock.Lease.
func (l *countingLease) ID() string { return l.id }

// KeepAlive implements lock.Lease, counting the call and signalling it.
func (l *countingLease) KeepAlive(ctx context.Context) error {
	l.mu.Lock()
	l.renewals++
	l.mu.Unlock()
	select {
	case l.renewedIn <- struct{}{}:
	default:
	}
	return ctx.Err()
}

// Release implements lock.Lease, counting the call.
func (l *countingLease) Release(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.releases++
	return nil
}

// counts returns the renewals and releases so far.
func (l *countingLease) counts() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.renewals, l.releases
}

func TestPollInterval(t *testing.T) {
	two, zero := TimeoutSeconds(2), TimeoutSeconds(0)
	for name, tc := range map[string]struct {
		task Task
		want time.Duration
	}{
		"not async":       {Task{}, 0},
		"no poll":         {Task{Async: 10}, defaultPoll},
		"poll of 2":       {Task{Async: 10, Poll: &two}, 2 * time.Second},
		"negative async":  {Task{Async: -1}, 0},
		"poll w/o async":  {Task{Poll: &two}, 0},
		"background, p=0": {Task{Async: 10, Poll: &zero}, 0},
	} {
		if got := tc.task.pollInterval(); got != tc.want {
			t.Errorf("%s: pollInterval = %s, want %s", name, got, tc.want)
		}
	}
}

func TestAsyncJobs_ClaimRefusesWhatIsNotThisDevicesOwnUncollectedJob(t *testing.T) {
	var jobs asyncJobs
	jobs.add(&asyncJob{id: "j1", device: "d1", done: make(chan struct{})})
	for name, tc := range map[string]struct{ id, device, want string }{
		"unknown":        {"nope", "d1", "no background job"},
		"another device": {"j1", "d2", "belongs to another device"},
	} {
		if _, err := jobs.claim(tc.id, tc.device); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: claim = %v, want one saying %q", name, err, tc.want)
		}
	}
	if _, err := jobs.claim("j1", "d1"); err != nil {
		t.Fatalf("the device's own job: %v", err)
	}
	if _, err := jobs.claim("j1", "d1"); err == nil || !strings.Contains(err.Error(), "already collected") {
		t.Errorf("a second claim = %v, want already collected", err)
	}
	if left := jobs.uncollected(); len(left) != 0 {
		t.Errorf("uncollected = %d jobs after the claim, want 0", len(left))
	}
}

// TestHeldLeases_ShareOneLeaseAndReleaseItWithTheLastHolder: a second job's
// lease on a held device is released at once and the held one shared; the
// shares answer the held lease's id and leave renewal to it; the lease is
// released only when its last share is, and each share releases once.
func TestHeldLeases_ShareOneLeaseAndReleaseItWithTheLastHolder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var h heldLeases
	if h.leaseOf("d1") != nil || h.join("d1") != nil {
		t.Fatal("a device nothing holds has a lease")
	}
	first := &countingLease{id: "L1", renewedIn: make(chan struct{}, 1)}
	second := &countingLease{id: "L2", renewedIn: make(chan struct{}, 1)}
	a := h.adopt(ctx, "d1", first, time.Hour)
	b := h.adopt(ctx, "d1", second, time.Hour)
	if _, released := second.counts(); released != 1 {
		t.Errorf("the second job's own lease was released %d times, want 1 (the held one is shared)", released)
	}
	joined := h.join("d1")
	if a.ID() != "L1" || b.ID() != "L1" || joined.ID() != "L1" {
		t.Errorf("share ids %q %q %q, want the held lease's L1", a.ID(), b.ID(), joined.ID())
	}
	if err := a.KeepAlive(ctx); err != nil {
		t.Errorf("a share's KeepAlive = %v; the held lease renews itself", err)
	}
	for i, share := range []interface{ Release(context.Context) error }{a, a, b} {
		if err := share.Release(ctx); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
		if _, released := first.counts(); released != 0 {
			t.Fatalf("the held lease was released with a share still out (after release %d)", i)
		}
	}
	if err := joined.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, released := first.counts(); released != 1 {
		t.Errorf("the held lease was released %d times after its last share, want 1", released)
	}
	if h.leaseOf("d1") != nil {
		t.Error("the device still reads held after its last share let go")
	}
}

// TestKeepAlive_RenewsUntilStoppedOrItsContextEnds.
func TestKeepAlive_RenewsUntilStoppedOrItsContextEnds(t *testing.T) {
	for _, how := range []string{"stop", "context"} {
		lease := &countingLease{id: "L", renewedIn: make(chan struct{}, 1)}
		ctx, cancel := context.WithCancel(context.Background())
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			keepAlive(ctx, lease, 3*time.Second, stop)
			close(done)
		}()
		select {
		case <-lease.renewedIn:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the lease was never renewed", how)
		}
		if how == "stop" {
			close(stop)
		} else {
			cancel()
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: keepAlive did not return", how)
		}
		cancel()
	}
}

func TestLimit_IsZeroOnlyWhenItSaysNothing(t *testing.T) {
	none, err := ParseLimit("  ")
	if err != nil || !none.IsZero() {
		t.Errorf("an empty limit: IsZero %v, err %v; want true", none.IsZero(), err)
	}
	some, err := ParseLimit("web")
	if err != nil || some.IsZero() {
		t.Errorf("limit web: IsZero %v, err %v; want false", some.IsZero(), err)
	}
}

// TestRunResult_AbsorbAppendsALaterBatch: a later batch's results follow,
// its secrets are added once, its metadata wins, and its run id and last
// sequence carry.
func TestRunResult_AbsorbAppendsALaterBatch(t *testing.T) {
	r := RunResult{Nodes: []NodeResult{{NodeID: "a"}}, Secrets: []string{"s1"}, RunID: "run", Sequence: 1}
	r.Absorb(RunResult{
		Nodes: []NodeResult{{NodeID: "b"}}, Secrets: []string{"s1", "s2"},
		Metadata: map[string]interface{}{"k": "v"}, RunID: "run", Sequence: 4,
	})
	if len(r.Nodes) != 2 || r.Nodes[1].NodeID != "b" {
		t.Errorf("nodes %+v, want a then b", r.Nodes)
	}
	if strings.Join(r.Secrets, ",") != "s1,s2" {
		t.Errorf("secrets %v, want s1,s2 once each", r.Secrets)
	}
	if r.Metadata["k"] != "v" || r.Sequence != 4 || r.RunID != "run" {
		t.Errorf("metadata %v, sequence %d, run %q", r.Metadata, r.Sequence, r.RunID)
	}
}

func TestContainsInt(t *testing.T) {
	if !containsInt([]int{1, 3}, 3) || containsInt([]int{1, 3}, 2) || containsInt(nil, 0) {
		t.Error("containsInt answered wrong")
	}
}
