// Package event_test: the per-job control channel, tested against both
// implementations of the same contract.
//
// The NATS cases run against a real ephemeral broker rather than a double.
// Per AGENTS.md RULE 0 that is not optional here: the whole reason this
// channel is core NATS rather than Bus is a claim about how the broker
// delivers, that every listening subscriber receives the message instead
// of one member of a consumer group. A mocked transport would assert the
// opposite of what is being claimed and pass.
package event_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
)

// controlTestJobID and otherControlJobID are two distinct jobs, for the
// cases that prove a signal reaches one and not the other.
const (
	controlTestJobID  = "0f1e2d3c-4b5a-4968-8776-655443322110"
	otherControlJobID = "11223344-5566-4778-899a-bbccddeeff00"
)

// awaitTrue polls flag until it is set. Used instead of a fixed sleep for
// the cases whose correct outcome is that something happens.
//
// The deadline is deliberately far longer than delivery takes, which is
// milliseconds against a local broker. It is sized for the worst case this
// suite actually produces rather than the typical one: a full parallel run
// boots a real container per test across several packages at once, and a
// two-second budget failed once under exactly that load while passing
// every time in isolation, which is FAILURE_PATTERNS.md #61's signature.
// A tight deadline there does not detect a slow product, it reports a
// scheduling delay as a broken cancel. Waiting longer costs nothing on a
// passing run, because a passing run returns as soon as the flag is set.
func awaitTrue(t *testing.T, flag *atomic.Bool, what string) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		if flag.Load() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// natsControl dials the broker and returns a control channel over it.
func natsControlChannel(t *testing.T, url string) event.CancelChannel {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect to nats: %v", err)
	}
	t.Cleanup(nc.Close)
	return event.NewNATSControl(nc)
}

// TestNATSControl_CancelReachesEverySubscriber is the property that
// decided this channel's design, asserted against a real broker.
//
// If this were built on event.Bus, both subscribers would share one
// durable consumer and exactly one of them would receive the signal. In a
// fleet that means a cancel handed to an arbitrary Runner rather than the
// one holding the job: nothing stops, and nothing reports that nothing
// stopped. Two subscribers here is the smallest arrangement that can tell
// the two delivery models apart.
func TestNATSControl_CancelReachesEverySubscriber(t *testing.T) {
	url := startNatsContainer(t)

	first := natsControlChannel(t, url)
	second := natsControlChannel(t, url)

	var firstSaw, secondSaw atomic.Bool
	unsubFirst, err := first.SubscribeCancel(context.Background(), controlTestJobID, func() { firstSaw.Store(true) })
	if err != nil {
		t.Fatalf("first SubscribeCancel: %v", err)
	}
	defer unsubFirst()
	unsubSecond, err := second.SubscribeCancel(context.Background(), controlTestJobID, func() { secondSaw.Store(true) })
	if err != nil {
		t.Fatalf("second SubscribeCancel: %v", err)
	}
	defer unsubSecond()

	if err := first.PublishCancel(context.Background(), controlTestJobID); err != nil {
		t.Fatalf("PublishCancel: %v", err)
	}

	awaitTrue(t, &firstSaw, "the first subscriber to receive the cancel")
	awaitTrue(t, &secondSaw, "the second subscriber to receive the cancel: a consumer group would have delivered to only one")
}

// TestNATSControl_CancelIsScopedToItsJob proves a signal for one job does
// not stop another.
//
// A Runner's subscribe grant names the whole control space, because a
// grant is minted before anyone knows which job that Runner will get, so
// it genuinely receives traffic for jobs it is not running. Without the
// per-job subject and the id check behind it, cancelling any one job would
// abort every execution in the fleet.
func TestNATSControl_CancelIsScopedToItsJob(t *testing.T) {
	url := startNatsContainer(t)
	control := natsControlChannel(t, url)

	var mine, theirs atomic.Bool
	unsubMine, err := control.SubscribeCancel(context.Background(), controlTestJobID, func() { mine.Store(true) })
	if err != nil {
		t.Fatalf("SubscribeCancel: %v", err)
	}
	defer unsubMine()
	unsubTheirs, err := control.SubscribeCancel(context.Background(), otherControlJobID, func() { theirs.Store(true) })
	if err != nil {
		t.Fatalf("SubscribeCancel: %v", err)
	}
	defer unsubTheirs()

	if err := control.PublishCancel(context.Background(), controlTestJobID); err != nil {
		t.Fatalf("PublishCancel: %v", err)
	}

	awaitTrue(t, &mine, "the cancelled job's own subscriber")
	if theirs.Load() {
		t.Error("a cancel for one job reached a subscriber watching a different job")
	}
}

// TestNATSControl_UnsubscribeStops proves the subscription really ends,
// so a Runner that has finished a job is not still listening for its
// cancellation.
func TestNATSControl_UnsubscribeStops(t *testing.T) {
	url := startNatsContainer(t)
	control := natsControlChannel(t, url)

	var saw atomic.Bool
	unsubscribe, err := control.SubscribeCancel(context.Background(), controlTestJobID, func() { saw.Store(true) })
	if err != nil {
		t.Fatalf("SubscribeCancel: %v", err)
	}
	unsubscribe()
	// Twice, because executeWithLease's defer can run on a path where the
	// caller has already released it, and a second call must not panic.
	unsubscribe()

	if err := control.PublishCancel(context.Background(), controlTestJobID); err != nil {
		t.Fatalf("PublishCancel: %v", err)
	}

	// A fixed wait, because the correct outcome here is that nothing
	// happens, and there is no event to wait for.
	time.Sleep(250 * time.Millisecond)
	if saw.Load() {
		t.Error("a cancel reached a subscriber that had already unsubscribed")
	}
}

// TestNATSControl_NoSubscriberIsNotAnError pins the honest limit of this
// channel: a cancel published while nothing is listening is silently lost,
// and the publisher is told nothing.
//
// This is not a defect to fix, it is the contract, and it is why every
// description of job cancel calls this half best effort. Pinning it in a
// test is what stops somebody later reading a nil error as delivery.
func TestNATSControl_NoSubscriberIsNotAnError(t *testing.T) {
	url := startNatsContainer(t)
	control := natsControlChannel(t, url)

	if err := control.PublishCancel(context.Background(), controlTestJobID); err != nil {
		t.Fatalf("PublishCancel with nobody listening = %v, want nil: a core publish is not acknowledged", err)
	}
}

// TestNATSControl_PublishesUnderTheDeclaredSubject proves the channel uses
// topology's own builder rather than a subject of its own invention.
//
// It matters because the Runner's subscribe grant and the Controller's
// publish grant both name topology.ControlSubjectAll(). A channel
// publishing anywhere else would be denied in a secured deployment, and a
// denied core subscribe reports nothing, so the failure would be silent.
func TestNATSControl_PublishesUnderTheDeclaredSubject(t *testing.T) {
	url := startNatsContainer(t)
	control := natsControlChannel(t, url)

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect to nats: %v", err)
	}
	defer nc.Close()

	// A raw subscription on the wildcard the grants name, so this asserts
	// against the permission boundary rather than against the builder
	// calling itself.
	received := make(chan string, 1)
	sub, err := nc.Subscribe(topology.ControlSubjectAll(), func(msg *nats.Msg) {
		select {
		case received <- msg.Subject:
		default:
		}
	})
	if err != nil {
		t.Fatalf("failed to subscribe to the control wildcard: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// Flushed before publishing, for the same reason SubscribeCancel
	// itself flushes: nats.go writes the SUB line asynchronously, and a
	// core publish that arrives before the server has registered the
	// subscription is dropped permanently rather than delivered late.
	// Without this the test carries the very race the implementation was
	// just fixed for, and fails for its full timeout roughly one run in
	// three.
	if err := nc.Flush(); err != nil {
		t.Fatalf("failed to flush the control wildcard subscription: %v", err)
	}

	if err := control.PublishCancel(context.Background(), controlTestJobID); err != nil {
		t.Fatalf("PublishCancel: %v", err)
	}

	select {
	case subject := <-received:
		if want := topology.ControlSubject(controlTestJobID); subject != want {
			t.Errorf("published on %q, want %q", subject, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("nothing arrived on %q, so the Runner's subscribe grant would never see this cancel", topology.ControlSubjectAll())
	}
}

// TestInProcessControl_MatchesTheNATSContract runs the in-memory
// implementation through the same properties, so the two cannot drift.
//
// It is the implementation every unit test and a single-process
// deployment use, and a divergence between them would mean the tests that
// are cheap to run prove something about a channel production does not
// have.
func TestInProcessControl_MatchesTheNATSContract(t *testing.T) {
	control := event.NewInProcessControl()

	t.Run("reaches every subscriber", func(t *testing.T) {
		var first, second atomic.Bool
		unsubFirst, err := control.SubscribeCancel(context.Background(), controlTestJobID, func() { first.Store(true) })
		if err != nil {
			t.Fatalf("SubscribeCancel: %v", err)
		}
		defer unsubFirst()
		unsubSecond, err := control.SubscribeCancel(context.Background(), controlTestJobID, func() { second.Store(true) })
		if err != nil {
			t.Fatalf("SubscribeCancel: %v", err)
		}
		defer unsubSecond()

		if err := control.PublishCancel(context.Background(), controlTestJobID); err != nil {
			t.Fatalf("PublishCancel: %v", err)
		}
		if !first.Load() || !second.Load() {
			t.Errorf("subscribers saw first=%v second=%v, want both", first.Load(), second.Load())
		}
	})

	t.Run("is scoped to its job", func(t *testing.T) {
		var theirs atomic.Bool
		unsubscribe, err := control.SubscribeCancel(context.Background(), otherControlJobID, func() { theirs.Store(true) })
		if err != nil {
			t.Fatalf("SubscribeCancel: %v", err)
		}
		defer unsubscribe()

		if err := control.PublishCancel(context.Background(), controlTestJobID); err != nil {
			t.Fatalf("PublishCancel: %v", err)
		}
		if theirs.Load() {
			t.Error("a cancel for one job reached a subscriber watching a different job")
		}
	})

	t.Run("unsubscribing stops delivery, twice over", func(t *testing.T) {
		var saw atomic.Bool
		unsubscribe, err := control.SubscribeCancel(context.Background(), controlTestJobID, func() { saw.Store(true) })
		if err != nil {
			t.Fatalf("SubscribeCancel: %v", err)
		}
		unsubscribe()
		unsubscribe()

		if err := control.PublishCancel(context.Background(), controlTestJobID); err != nil {
			t.Fatalf("PublishCancel: %v", err)
		}
		if saw.Load() {
			t.Error("a cancel reached a subscriber that had already unsubscribed")
		}
	})

	t.Run("a callback may unsubscribe itself", func(t *testing.T) {
		// The Runner's own callback is cancelExec, whose deferred
		// unsubscribe runs on the same path. A lock held across the
		// callback would deadlock here rather than fail, so this case
		// hangs rather than reporting if it regresses, which is why the
		// implementation copies its watchers out before calling them.
		var unsubscribe func()
		done := make(chan struct{})
		var err error
		unsubscribe, err = control.SubscribeCancel(context.Background(), controlTestJobID, func() {
			unsubscribe()
			close(done)
		})
		if err != nil {
			t.Fatalf("SubscribeCancel: %v", err)
		}

		if err := control.PublishCancel(context.Background(), controlTestJobID); err != nil {
			t.Fatalf("PublishCancel: %v", err)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("a callback that unsubscribed itself never completed")
		}
	})
}
