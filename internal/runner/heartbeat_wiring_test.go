// Tests for the three contracts the heartbeat work left unexercised: the
// option that wires a Heartbeat into an Agent, the context wrapper that
// lets a non-interruptible execution outlive Agent.Run's shutdown, and the
// message an operator reads off a failed probe.
//
// These are in-package for one reason each. WithHeartbeat's whole claim is
// about a field nothing exported can observe; detachedValueContext is
// unexported; and StaleHeartbeatError's message is asserted against the
// struct's own fields rather than against a string a caller happened to
// build. heartbeat_test.go's fakeConsumerProbe is reused rather than
// copied, which is the reason this file sits beside it rather than in
// runner_test.
package runner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestWithHeartbeatWiresTheAgentsLiveness covers both branches of the
// option, and getting the nil one to mean anything took two attempts.
//
// WithHeartbeat's doc comment promises that "a nil hb is ignored". The
// obvious test for that builds an Agent with WithHeartbeat(nil) and
// asserts liveness is nil, and it CANNOT FAIL: liveness is a concrete
// *Heartbeat, so the unguarded assignment stores nil and the guarded one
// stores nothing, and the field is nil either way. Verified by deleting
// the guard and watching that version stay green.
//
// The guard's one observable effect is on ORDER. Options are applied in
// sequence, so without it a later WithHeartbeat(nil) silently clears a
// heartbeat an earlier option already set, which is how a composition root
// that passes an optional heartbeat through a slice of options ends up
// with a Runner whose probe never has anything to read. That is what the
// second case below asserts, and it fails when the guard is removed.
func TestWithHeartbeatWiresTheAgentsLiveness(t *testing.T) {
	newHeartbeat := func(t *testing.T) *Heartbeat {
		t.Helper()
		hb, err := NewHeartbeat(
			HeartbeatPath(filepath.Join(t.TempDir(), "heartbeat")),
			time.Second, &fakeConsumerProbe{}, nil)
		if err != nil {
			t.Fatalf("building a heartbeat: %v", err)
		}
		return hb
	}

	t.Run("a heartbeat is adopted", func(t *testing.T) {
		hb := newHeartbeat(t)
		agent := NewAgent(nil, &noopAdapter{}, nil, lock.NewInProcessManager(), 5, nil, nil,
			WithHeartbeat(hb))
		if agent.liveness != hb {
			t.Fatalf("the agent's liveness is %v, want the heartbeat it was built with", agent.liveness)
		}
	})

	t.Run("a nil heartbeat does not clear one already set", func(t *testing.T) {
		hb := newHeartbeat(t)
		agent := NewAgent(nil, &noopAdapter{}, nil, lock.NewInProcessManager(), 5, nil, nil,
			WithHeartbeat(hb), WithHeartbeat(nil))

		if agent.liveness != hb {
			t.Fatalf("a later WithHeartbeat(nil) cleared the heartbeat an earlier option set: liveness is %v, want %v. "+
				"A nil heartbeat is documented as IGNORED, which has to mean ignored wherever it appears in the "+
				"option list, or an optional heartbeat threaded through a shared option slice silently disables "+
				"the runner's whole liveness surface", agent.liveness, hb)
		}
	})

	t.Run("no option at all leaves liveness unset", func(t *testing.T) {
		agent := NewAgent(nil, &noopAdapter{}, nil, lock.NewInProcessManager(), 5, nil, nil)
		if agent.liveness != nil {
			t.Errorf("an agent built with no heartbeat option has liveness %v, want nil", agent.liveness)
		}
	})
}

// TestDetachedValueContextDropsCancellationAndKeepsValues is the contract
// executeWithLease depends on.
//
// The wrapper exists so a non-interruptible execution can outlive
// Agent.Run's own shutdown signal while still carrying what the parent
// context holds (an active OpenTelemetry span, in real use). Both halves
// are asserted against a parent that is ALREADY CANCELED, because a test
// against a live parent passes whether the wrapper detaches or not: an
// uncanceled parent reports no error either.
func TestDetachedValueContextDropsCancellationAndKeepsValues(t *testing.T) {
	type contextKey string
	const key contextKey = "span"

	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key, "carried"))
	cancel()

	// The negative control on the test itself: if the parent is not really
	// canceled, everything below proves nothing.
	if parent.Err() == nil {
		t.Fatal("the parent context is not canceled, so this test cannot distinguish a detached context from a passthrough")
	}

	detached := detachedValueContext{parent: parent}

	if got := detached.Err(); got != nil {
		t.Errorf("Err() = %v on a canceled parent, want nil: cancellation is exactly what this wrapper drops", got)
	}
	if deadline, ok := detached.Deadline(); ok || !deadline.IsZero() {
		t.Errorf("Deadline() = (%v, %v), want (zero, false)", deadline, ok)
	}
	if got := detached.Done(); got != nil {
		t.Errorf("Done() = %v, want nil: a nil channel never fires, which is how this context is never canceled by its parent", got)
	}
	if got := detached.Value(key); got != "carried" {
		t.Errorf("Value(%q) = %v, want %q: the values are the one thing this wrapper exists to keep", key, got, "carried")
	}

	// And the seam the doc comment describes: a cancel func built on top of
	// the detached context still works, so the execution is detached from
	// the parent rather than uncancellable.
	own, ownCancel := context.WithCancel(detached)
	if own.Err() != nil {
		t.Fatalf("a context built on the detached one starts canceled: %v", own.Err())
	}
	ownCancel()
	if !errors.Is(own.Err(), context.Canceled) {
		t.Errorf("own.Err() = %v, want context.Canceled: the detached context must still be cancellable deliberately", own.Err())
	}
}

// TestStaleHeartbeatErrorNamesWhatAnOperatorActsOn asserts the message
// carries all three numbers.
//
// The type's own doc comment says it "carries the numbers rather than only
// a message so a caller can act on them and a human reading a probe
// failure can tell 'one second over' from 'the broker has been gone for an
// hour'". A message that dropped the age or the limit would satisfy every
// other test in this package, because nothing else reads it.
func TestStaleHeartbeatErrorNamesWhatAnOperatorActsOn(t *testing.T) {
	err := &StaleHeartbeatError{
		Path:   HeartbeatPath("/tmp/pleiades-runner/heartbeat"),
		Age:    90 * time.Second,
		MaxAge: time.Minute,
	}

	message := err.Error()
	for _, want := range []string{
		"/tmp/pleiades-runner/heartbeat", // which file
		"1m30s",                          // how stale
		"1m0s",                           // the limit it passed
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not contain %q, so a probe failure cannot be acted on from it alone:\n%s", want, message)
		}
	}

	// The distinction the type exists to draw, stated as a test: this is
	// not the same condition as a heartbeat that was never written.
	if errors.Is(err, ErrHeartbeatMissing) {
		t.Error("a stale heartbeat matches ErrHeartbeatMissing; the two mean different things and a caller " +
			"telling a cold start from a severed broker depends on them staying distinct")
	}
}

// noopAdapter is an ExecutionAdapter that is never called: every test in
// this file builds an Agent to inspect how it was configured and never
// runs it.
type noopAdapter struct{}

func (noopAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) error {
	return errors.New("noopAdapter is not meant to run")
}
