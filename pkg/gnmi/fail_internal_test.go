// Package gnmi: a call whose deadline passed is said as its deadline, even
// when gRPC reports DeadlineExceeded a moment before the context's own timer
// marks the context done.
package gnmi

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// lateContext is a context whose deadline has passed and whose Done closes a
// moment later, as a timer context's does while its timer goroutine runs.
type lateContext struct {
	context.Context
	deadline time.Time
	done     chan struct{}
}

// Deadline implements context.Context.
func (c lateContext) Deadline() (time.Time, bool) { return c.deadline, true }

// Done implements context.Context.
func (c lateContext) Done() <-chan struct{} { return c.done }

// Err implements context.Context.
func (c lateContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// TestFail_ADeadlinePassedIsTheDeadline hands fail the status gRPC gives a
// call the server reset just as its deadline passed, before the context is
// marked done, and holds it to the deadline rather than the server's answer.
func TestFail_ADeadlinePassedIsTheDeadline(t *testing.T) {
	ctx := lateContext{Context: context.Background(), deadline: time.Now().Add(-time.Millisecond), done: make(chan struct{})}
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(ctx.done)
	}()
	c := &Client{}
	err := c.fail(ctx, "Capabilities", status.Error(codes.DeadlineExceeded, "stream terminated by RST_STREAM with error code: CANCEL"))
	var e *Error
	if errors.As(err, &e) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fail = %v, want the call's deadline, not the server's answer", err)
	}
}
