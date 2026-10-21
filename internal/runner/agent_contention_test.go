// Package runner_test: a dispatch whose device is held ends one of three
// ways, and a dispatch in hand is reported in progress until it is settled
// (Phase 103a).
package runner_test

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// holdingJobID is a run other than testJobID's holding the device.
const holdingJobID = "6f1c2a7e-0b3d-4c5e-9f8a-1b2c3d4e5f60"

// deliveredMsg is a MockMsg on its n-th delivery that records how it was
// nak'd and how often it was reported in progress.
type deliveredMsg struct {
	*MockMsg
	delivered uint64
	nakDelay  atomic.Int64
	naked     atomic.Bool
	progress  atomic.Int32
}

func (m *deliveredMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{NumDelivered: m.delivered}, nil
}

func (m *deliveredMsg) NakWithDelay(d time.Duration) error {
	m.nakDelay.Store(int64(d))
	m.naked.Store(true)
	return nil
}

func (m *deliveredMsg) InProgress() error {
	m.progress.Add(1)
	return nil
}

// contended holds device under owner and runs one delivery of a dispatch
// for it, the delivered-th, returning the message and what results the
// Agent published.
func contended(t *testing.T, device, owner string, delivered uint64, opts ...runner.AgentOption) (*deliveredMsg, <-chan runner.ResultEntry) {
	t.Helper()
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wal.Close() })
	bus := event.NewInProcessBus()
	t.Cleanup(func() { _ = bus.Close() })
	received := subscribeResultEntries(t, bus, testJobID)

	locks := lock.NewInProcessManager()
	held, err := locks.Acquire(context.Background(), device, time.Minute, lock.AcquireOptions{Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Release(context.Background()) })

	msg := &deliveredMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON(device))}, delivered: delivered}
	agent := runner.NewAgent(&MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}, &MockAdapter{}, nil, locks, 5, slog.Default(), nil,
		append([]runner.AgentOption{runner.WithResultWAL(wal, bus)}, opts...)...)
	runAgentUntil(t, agent, func() bool { return msg.naked.Load() || msg.term.Load() }, agentSettleTimeout)
	return msg, received
}

// noResult waits long enough for an asynchronous publish to have arrived.
func noResult(t *testing.T, received <-chan runner.ResultEntry) {
	t.Helper()
	select {
	case entry := <-received:
		t.Fatalf("a dispatch that never ran reported %+v", entry)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestContention_AnotherRunWaitsOnTheContentionSchedule: another run's
// hold, with deliveries left, is waited out on retry.Backoff(15s, 2m, n):
// about a minute on the second delivery, not the fetch loop's 400 ms.
func TestContention_AnotherRunWaitsOnTheContentionSchedule(t *testing.T) {
	msg, received := contended(t, "device-busy-wait", topology.DispatchKey(holdingJobID, "device-busy-wait"), 2)
	if d := time.Duration(msg.nakDelay.Load()); d < 60*time.Second || d > 66*time.Second {
		t.Errorf("the second delivery waits %v, want 60s plus up to 10%% jitter", d)
	}
	if msg.ack.Load() || msg.term.Load() {
		t.Error("a dispatch waiting for another run was settled")
	}
	noResult(t, received)
}

// TestContention_ItsOwnRunningDispatchWaitsOneLeaseTTL: a delivery that
// finds its own dispatch holding the device (work already running) waits
// one lease TTL and reports nothing, even on the final delivery, since the
// running copy reports.
func TestContention_ItsOwnRunningDispatchWaitsOneLeaseTTL(t *testing.T) {
	const device = "device-own-run"
	msg, received := contended(t, device, topology.DispatchKey(testJobID, device), 5, runner.WithLeaseTTL(42*time.Second))
	if d := time.Duration(msg.nakDelay.Load()); d != 42*time.Second {
		t.Errorf("a duplicate of a running dispatch waits %v, want the lease TTL, 42s", d)
	}
	if msg.term.Load() {
		t.Error("a duplicate of a running dispatch was terminated")
	}
	noResult(t, received)
}

// TestContention_TheFinalDeliveryReportsTheDeviceBusy: on the final
// delivery another run's hold ends the dispatch failed, naming the job
// that holds the device, and the message is terminated, so the job ends
// rather than staying running.
func TestContention_TheFinalDeliveryReportsTheDeviceBusy(t *testing.T) {
	for _, tc := range []struct {
		name, owner, want string
	}{
		{"a dispatch key", topology.DispatchKey(holdingJobID, "device-busy-final"), "device busy: held by job " + holdingJobID},
		{"no owner", "", "device busy: held by a run that did not record which job it is"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg, received := contended(t, "device-busy-final", tc.owner, 5)
			if !msg.term.Load() || msg.naked.Load() {
				t.Fatalf("the final delivery was not terminated (term %v, nak %v)", msg.term.Load(), msg.naked.Load())
			}
			select {
			case entry := <-received:
				if entry.Outcome != "failed" || entry.Reason != tc.want {
					t.Errorf("reported %q, %q; want failed, %q", entry.Outcome, entry.Reason, tc.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the busy device was never reported")
			}
		})
	}
}

// slowAdapter takes d to execute.
type slowAdapter struct{ d time.Duration }

func (s slowAdapter) Execute(ctx context.Context, _ wire.DispatchPayload) (wire.Outcome, error) {
	select {
	case <-time.After(s.d):
	case <-ctx.Done():
	}
	return wire.Outcome{}, nil
}

// TestProgress_ReportedUntilTheDispatchIsSettled: a dispatch in hand is
// reported in progress while it runs, and never after it is acked.
func TestProgress_ReportedUntilTheDispatchIsSettled(t *testing.T) {
	msg := &deliveredMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-slow"))}, delivered: 1}
	agent := runner.NewAgent(&MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}, slowAdapter{d: 300 * time.Millisecond}, nil,
		lock.NewInProcessManager(), 5, slog.Default(), nil, runner.WithProgressInterval(20*time.Millisecond))
	runAgentUntil(t, agent, msg.ack.Load, agentSettleTimeout)
	reported := msg.progress.Load()
	if reported < 3 {
		t.Errorf("a 300 ms run was reported in progress %d times at a 20 ms interval, want several", reported)
	}
	time.Sleep(100 * time.Millisecond)
	if after := msg.progress.Load(); after != reported {
		t.Errorf("reported in progress %d more times after the ack", after-reported)
	}
	if strings.Contains(time.Duration(msg.nakDelay.Load()).String(), "-") {
		t.Error("impossible negative nak delay")
	}
}
