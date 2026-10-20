// Tests for retryUnreached: a call is tried again only while its report
// says the device never answered, within its bound, and never after an
// answer of any kind.
package oscheck

import (
	"context"
	"testing"
	"time"
)

// The reports a call can return, as adhoc --json prints them.
var (
	unreachedReport = []byte(`{"tasks":[{"status":"failed","error":"remoteexec: dial h:22: i/o timeout","unreached":true}],"outcome":{"exit_code":1}}`)
	passedReport    = []byte(`{"tasks":[{"status":"ok","stats":{"stdout":"up"}}],"outcome":{"exit_code":0}}`)
	refusedReport   = []byte(`{"tasks":[{"status":"failed","error":"ssh: unable to authenticate"}],"outcome":{"exit_code":1}}`)
	failedReport    = []byte(`{"tasks":[{"status":"failed","error":"exec.command: command exited 3","stats":{"rc":3}}],"outcome":{"exit_code":1}}`)
)

// fakeWaits records each wait instead of sleeping.
type fakeWaits struct{ waits []time.Duration }

func (f *fakeWaits) wait(_ context.Context, d time.Duration) bool {
	f.waits = append(f.waits, d)
	return true
}

// replies returns a call that answers each report in turn, the last one
// from then on, counting the calls.
func replies(calls *int, reports ...[]byte) func() []byte {
	return func() []byte {
		*calls++
		if *calls <= len(reports) {
			return reports[*calls-1]
		}
		return reports[len(reports)-1]
	}
}

// TestRetryUnreached_TriesAgainUntilTheDeviceAnswers: two unreached reports
// then a pass returns the pass after two waits, backing off.
func TestRetryUnreached_TriesAgainUntilTheDeviceAnswers(t *testing.T) {
	var calls int
	w := &fakeWaits{}
	out := retryUnreached(context.Background(), UnreachedRetryFor, w.wait, replies(&calls, unreachedReport, unreachedReport, passedReport))
	if string(out) != string(passedReport) || calls != 3 {
		t.Fatalf("calls %d, out %s", calls, out)
	}
	if len(w.waits) != 2 || w.waits[0] != 2*time.Second || w.waits[1] != 4*time.Second {
		t.Errorf("waits %v, want 2s then 4s", w.waits)
	}
}

// TestRetryUnreached_NeverRepeatsAnAnswer: a pass, a refused login and a
// command that ran and failed are each returned from the first call.
func TestRetryUnreached_NeverRepeatsAnAnswer(t *testing.T) {
	for name, report := range map[string][]byte{"passed": passedReport, "refused login": refusedReport, "command failed": failedReport, "no report": []byte("not json")} {
		var calls int
		w := &fakeWaits{}
		if out := retryUnreached(context.Background(), UnreachedRetryFor, w.wait, replies(&calls, report)); string(out) != string(report) || calls != 1 || len(w.waits) != 0 {
			t.Errorf("%s: calls %d, waits %v", name, calls, w.waits)
		}
	}
}

// TestRetryUnreached_StopsAtItsBound: a device that never answers is tried
// until the waits would pass the bound, which with doubling from 2 s to 30 s
// is 2+4+8+16 and then five waits of 30 s, three minutes exactly, then the
// last unreached report is returned.
func TestRetryUnreached_StopsAtItsBound(t *testing.T) {
	var calls int
	w := &fakeWaits{}
	out := retryUnreached(context.Background(), UnreachedRetryFor, w.wait, replies(&calls, unreachedReport))
	if string(out) != string(unreachedReport) {
		t.Fatalf("out %s", out)
	}
	var total time.Duration
	for _, d := range w.waits {
		total += d
		if d > unreachedLongWait {
			t.Errorf("a wait of %s, past the longest", d)
		}
	}
	if total != UnreachedRetryFor || len(w.waits) != 9 || calls != len(w.waits)+1 {
		t.Errorf("waited %s over %d waits in %d calls", total, len(w.waits), calls)
	}
}

// TestRetryUnreached_StopsWhenTheRunIsStopped: an ended context, or a wait
// the context cut short, returns at once.
func TestRetryUnreached_StopsWhenTheRunIsStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	if retryUnreached(ctx, UnreachedRetryFor, waitFor, replies(&calls, unreachedReport)); calls != 1 {
		t.Errorf("an ended run called %d times", calls)
	}
	calls = 0
	cut := func(context.Context, time.Duration) bool { return false }
	if retryUnreached(context.Background(), UnreachedRetryFor, cut, replies(&calls, unreachedReport)); calls != 1 {
		t.Errorf("a cut wait called %d times", calls)
	}
}
