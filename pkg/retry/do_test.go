package retry_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
)

// noSleep is a delay function tests use when they do not want to wait out
// real backoff timing.
func noSleep(int) time.Duration { return 0 }

var errFlaky = errors.New("simulated flaky failure")

// TestDo_SucceedsOnFirstAttempt proves fn is called exactly once when it
// succeeds immediately, with no delay call and no retry.
func TestDo_SucceedsOnFirstAttempt(t *testing.T) {
	var calls int32
	var delayCalls int32
	fn := func(context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "ok", nil
	}
	delay := func(attempt int) time.Duration {
		atomic.AddInt32(&delayCalls, 1)
		return 0
	}

	got, err := retry.Do(context.Background(), delay, 3, func(error) bool { return true }, fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("got %q, want %q", got, "ok")
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
	if delayCalls != 0 {
		t.Fatalf("delay called %d times, want 0 (no retry needed)", delayCalls)
	}
}

// TestDo_RetriesUntilSuccess proves a retryable failure is retried, and
// that the eventual success value and nil error are what Do returns.
func TestDo_RetriesUntilSuccess(t *testing.T) {
	var calls int32
	fn := func(context.Context) (int, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return 0, errFlaky
		}
		return 42, nil
	}

	got, err := retry.Do(context.Background(), noSleep, 5, func(error) bool { return true }, fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
	if calls != 3 {
		t.Fatalf("fn called %d times, want 3", calls)
	}
}

// TestDo_NonRetryableErrorStopsImmediately proves an error retryable
// rejects is returned as-is, unwrapped, with no further attempts and no
// delay call.
func TestDo_NonRetryableErrorStopsImmediately(t *testing.T) {
	var calls int32
	sentinel := errors.New("do not retry this")
	fn := func(context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, sentinel
	}
	delayCalled := false
	delay := func(int) time.Duration { delayCalled = true; return 0 }

	_, err := retry.Do(context.Background(), delay, 5, func(error) bool { return false }, fn)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want it to wrap %v", err, sentinel)
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want exactly 1", calls)
	}
	if delayCalled {
		t.Error("delay must not be called when the failure is not retryable")
	}
}

// TestDo_ExhaustsMaxAttempts proves a positive maxAttempts caps the total
// number of calls to fn, with no sleep after the final attempt.
func TestDo_ExhaustsMaxAttempts(t *testing.T) {
	var calls int32
	fn := func(context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errFlaky
	}
	var delayCalls []int
	delay := func(attempt int) time.Duration {
		delayCalls = append(delayCalls, attempt)
		return 0
	}

	_, err := retry.Do(context.Background(), delay, 3, func(error) bool { return true }, fn)
	if err == nil {
		t.Fatal("expected an error once attempts are exhausted")
	}
	if !errors.Is(err, errFlaky) {
		t.Errorf("err = %v, want it to wrap the last attempt's error", err)
	}
	if calls != 3 {
		t.Fatalf("fn called %d times, want exactly 3 (maxAttempts)", calls)
	}
	// Two sleeps between three attempts (after attempt 0 and after
	// attempt 1), never a third after the final, exhausting attempt.
	if want := []int{0, 1}; !equalInts(delayCalls, want) {
		t.Errorf("delay called with attempts %v, want %v", delayCalls, want)
	}
}

// TestDo_UnlimitedRetriesUntilContextDone proves maxAttempts <= 0 means
// unlimited: Do keeps retrying a permanently failing, retryable fn until
// ctx is done, and the returned error reports both the cancellation and
// the last attempt's own error.
func TestDo_UnlimitedRetriesUntilContextDone(t *testing.T) {
	var calls int32
	fn := func(context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errFlaky
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := retry.Do(ctx, func(int) time.Duration { return 5 * time.Millisecond }, 0, func(error) bool { return true }, fn)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if !errors.Is(err, errFlaky) {
		t.Errorf("err = %v, want it to also wrap the last attempt's own error", err)
	}
	if calls < 2 {
		t.Errorf("fn called only %d time(s); expected several retries before the deadline", calls)
	}
}

// TestDo_ContextAlreadyDoneStopsAfterOneAttempt proves Do does not pre-check
// ctx before the first call to fn (a caller like pkg/remoteexec's dial loop
// has its own reasons, a circuit breaker check, for ordering its own ctx
// check relative to other work inside fn), but that once fn returns while
// ctx is already done, Do stops deterministically after exactly one
// attempt rather than treating the error as retryable and racing a
// zero-delay sleep against an already-closed ctx.Done().
func TestDo_ContextAlreadyDoneStopsAfterOneAttempt(t *testing.T) {
	var calls int32
	fn := func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 1, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := retry.Do(ctx, noSleep, 3, func(error) bool { return true }, fn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want exactly 1: ctx ending must stop the loop immediately regardless of what retryable says", calls)
	}
}

// TestDo_ContextDoneWinsOverRetryableEvenWhenFnIgnoresCtx proves the
// ctx.Err() check is Do's own, not something it depends on fn to perform:
// fn here returns a plain, unrelated retryable error and never looks at
// ctx at all, yet Do still stops after one attempt once ctx is already
// done, rather than calling retryable (which would say "keep going").
func TestDo_ContextDoneWinsOverRetryableEvenWhenFnIgnoresCtx(t *testing.T) {
	var calls int32
	fn := func(context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errFlaky
	}
	retryableCalled := false
	retryable := func(error) bool { retryableCalled = true; return true }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := retry.Do(ctx, noSleep, 3, retryable, fn)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errFlaky) {
		t.Fatalf("err = %v, want it to wrap both context.Canceled and the fn error", err)
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want exactly 1", calls)
	}
	if retryableCalled {
		t.Error("retryable must not be consulted once ctx is already done")
	}
}

// TestDo_DelayReceivesTheFailedAttemptIndex proves attempt is the
// zero-based index of the attempt that just failed, matching
// retry.Backoff's own documented convention (attempt 0 is the delay
// before the very first retry).
func TestDo_DelayReceivesTheFailedAttemptIndex(t *testing.T) {
	var calls int32
	var seen []int
	fn := func(context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errFlaky
	}
	delay := func(attempt int) time.Duration {
		seen = append(seen, attempt)
		return 0
	}

	_, _ = retry.Do(context.Background(), delay, 4, func(error) bool { return true }, fn)

	if want := []int{0, 1, 2}; !equalInts(seen, want) {
		t.Fatalf("delay saw attempts %v, want %v", seen, want)
	}
}

// TestSleep_WaitsOutTheBackoffDelay proves Sleep returns nil once the
// jittered Backoff(base, max, attempt) delay elapses, taking at least that
// long.
func TestSleep_WaitsOutTheBackoffDelay(t *testing.T) {
	base := 20 * time.Millisecond
	start := time.Now()
	err := retry.Sleep(context.Background(), base, time.Second, 0)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < base {
		t.Errorf("Sleep returned after %v, want at least the base delay %v", elapsed, base)
	}
}

// TestSleep_ReturnsCtxErrorWhenCtxEndsFirst proves Sleep bails out with
// ctx's own error instead of waiting out the full delay once ctx is done
// first.
func TestSleep_ReturnsCtxErrorWhenCtxEndsFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := retry.Sleep(ctx, time.Hour, time.Hour, 0)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Sleep took %v, want it to return promptly once ctx ended rather than waiting out an hour-long delay", elapsed)
	}
}

// TestSleep_AlreadyDoneContextReturnsImmediately proves an already-done
// ctx is caught by the select without waiting for even a zero-length
// timer to fire on its own schedule.
func TestSleep_AlreadyDoneContextReturnsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := retry.Sleep(ctx, time.Hour, time.Hour, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// BenchmarkDo_SuccessOnFirstAttempt measures Do's own per-call overhead on
// the common path (no retry needed), the same shape
// pkg/remoteexec.dialWithRetry hits on every successful dial. This is
// what stands in for an A/B against the hand-rolled loop pkg/remoteexec
// and internal/lock each used to carry: that loop no longer exists as a
// separate implementation to benchmark against (Phase 72 replaced it
// rather than keeping a second copy around for comparison), so the
// evidence that the shared loop costs nothing meaningful is this number
// staying in the tens of nanoseconds with zero unexpected allocations,
// not a side-by-side of two loops doing the same three lines of work.
func BenchmarkDo_SuccessOnFirstAttempt(b *testing.B) {
	ctx := context.Background()
	fn := func(context.Context) (int, error) { return 1, nil }
	retryable := func(error) bool { return true }

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := retry.Do(ctx, noSleep, 3, retryable, fn); err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}
