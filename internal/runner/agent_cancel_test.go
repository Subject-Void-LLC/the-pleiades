// Package runner_test: the third cancellation trigger, an operator
// stopping the job, in its own sibling file beside agent_exec_test.go's
// coverage of the other two.
//
// These tests drive the real Agent.Run loop and the real executeWithLease,
// so what they exercise is the same path a dispatch takes in production.
// The control channel itself is event.NewInProcessControl, which is the
// real implementation of that port rather than a stand-in: the NATS one is
// covered against a live broker in internal/event.
package runner_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go/jetstream"
)

// otherJobID is a job this package's fixtures never dispatch, for the
// case that proves a cancel is scoped to one job.
//
// Spelled out here rather than written inline, because the obvious inline
// value to reach for is exactly testJobID's own
// "11111111-1111-4111-8111-111111111111", and a test that published to the
// job it was meant to leave alone would pass while asserting nothing.
const otherJobID = "22222222-2222-4222-8222-222222222222"

// TestAgent_CancelSignalAbortsAnInterruptibleExecution is the best-effort
// half of a job cancel, proven at the seam it actually uses.
//
// The signal is published while Execute is genuinely in flight, which is
// the only window in which cancelling means anything, and the assertion is
// that the running execution observed its context being canceled rather
// than that a function was called.
func TestAgent_CancelSignalAbortsAnInterruptibleExecution(t *testing.T) {
	locks := lock.NewInProcessManager()
	adapter := newSelfPacedAdapter()
	control := event.NewInProcessControl()

	msg := &MockMsg{data: wireWrapDispatchPayload(interruptiblePayloadJSON("device-cancel", true))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, locks, 5, slog.Default(), nil,
		runner.WithCancelSignals(control))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was never entered")
	}

	// The operator's cancel, arriving mid-execution. Published on the job
	// the dispatch names, which is what the Agent subscribed to.
	if err := control.PublishCancel(context.Background(), testJobID); err != nil {
		t.Fatalf("PublishCancel returned unexpected error: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for !adapter.sawCancel.Load() {
		select {
		case <-deadline:
			t.Fatal("Execute never observed its context being canceled after the job was canceled")
		case <-time.After(10 * time.Millisecond):
		}
	}

	close(adapter.done)
	cancel()
	select {
	case <-runErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
	}
}

// TestAgent_CancelSignalHonorsInterruptibleFalse is the exception, and it
// is the assertion that matters most in this file.
//
// PLAN.md Section 16 names one: a task declaring itself un-abortable
// finishes. That exists for work it is genuinely unsafe to stop halfway,
// mid-configuration-write on a switch being the concrete case. The other
// two triggers, an outer shutdown and a lost lease heartbeat, both honour
// it already; a third that did not would quietly make the flag mean
// "abortable by anything except a heartbeat failure".
//
// The job still records canceled and its fan-out still stops. What this
// proves is only how far the decision reaches into work already running.
func TestAgent_CancelSignalHonorsInterruptibleFalse(t *testing.T) {
	locks := lock.NewInProcessManager()
	adapter := newSelfPacedAdapter()
	control := event.NewInProcessControl()

	msg := &MockMsg{data: wireWrapDispatchPayload(interruptiblePayloadJSON("device-cancel-no-abort", false))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, locks, 5, slog.Default(), nil,
		runner.WithCancelSignals(control))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was never entered")
	}

	if err := control.PublishCancel(context.Background(), testJobID); err != nil {
		t.Fatalf("PublishCancel returned unexpected error: %v", err)
	}

	// Long enough that a propagating cancellation would have arrived. The
	// same 150ms agent_exec_test.go's own outer-shutdown case uses, for
	// the same reason: there is no event to wait for when the correct
	// behaviour is that nothing happens.
	time.Sleep(150 * time.Millisecond)
	if adapter.sawCancel.Load() {
		t.Fatal("Execute observed cancellation from an operator cancel despite Interruptible=false")
	}

	close(adapter.done) // let Execute finish on its own now
	cancel()
	select {
	case <-runErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after the non-interruptible execution finished on its own")
	}

	if !msg.ack.Load() {
		t.Fatal("message was never acked: Execute did not run to its own natural completion despite Interruptible=false")
	}
	if adapter.sawCancel.Load() {
		t.Fatal("Execute's context was canceled at some point during its run despite Interruptible=false")
	}
}

// TestAgent_CancelSignalForAnotherJobIsIgnored proves the subscription is
// scoped to the job this Agent is executing.
//
// A Runner's subscribe permission names the whole control space, because a
// grant cannot know which job it will be given, so a Runner really can
// receive traffic about jobs it is not running. Aborting on one of those
// would let cancelling any job stop every execution in the fleet.
func TestAgent_CancelSignalForAnotherJobIsIgnored(t *testing.T) {
	locks := lock.NewInProcessManager()
	adapter := newSelfPacedAdapter()
	control := event.NewInProcessControl()

	msg := &MockMsg{data: wireWrapDispatchPayload(interruptiblePayloadJSON("device-other-job", true))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, locks, 5, slog.Default(), nil,
		runner.WithCancelSignals(control))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was never entered")
	}

	if err := control.PublishCancel(context.Background(), otherJobID); err != nil {
		t.Fatalf("PublishCancel returned unexpected error: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if adapter.sawCancel.Load() {
		t.Fatal("Execute was aborted by a cancel published for a different job")
	}

	close(adapter.done)
	cancel()
	select {
	case <-runErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
	}
}

// TestAgent_CancelSignalAfterExecutionIsHarmless proves the subscription
// does not outlive the execution it belongs to.
//
// A Runner holding a subscription for a job it has finished would be one
// more thing leaking per dispatch, and a cancel arriving late would fire a
// callback against a context nobody is using. Publishing after Run has
// returned must simply do nothing.
func TestAgent_CancelSignalAfterExecutionIsHarmless(t *testing.T) {
	locks := lock.NewInProcessManager()
	adapter := newSelfPacedAdapter()
	control := event.NewInProcessControl()

	msg := &MockMsg{data: wireWrapDispatchPayload(interruptiblePayloadJSON("device-late-cancel", true))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, locks, 5, slog.Default(), nil,
		runner.WithCancelSignals(control))

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was never entered")
	}
	close(adapter.done)

	// Wait for the execution to finish before cancelling, so the
	// subscription has been unsubscribed by its own defer.
	deadline := time.After(5 * time.Second)
	for !msg.ack.Load() {
		select {
		case <-deadline:
			t.Fatal("the execution never completed")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if err := control.PublishCancel(context.Background(), testJobID); err != nil {
		t.Fatalf("PublishCancel after the execution returned unexpected error: %v", err)
	}

	cancel()
	select {
	case <-runErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
	}
}
