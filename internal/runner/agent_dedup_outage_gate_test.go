// Phase 96c's Release Gate: a dispatch published while its link is cut is
// executed exactly once, over real NATS behind a real Toxiproxy.
//
// goleak is deliberately absent, for the reason internal/event's own
// reconnect gate records: severing a broker leaves nats.go's reconnect
// machinery running by design (MaxReconnects(-1) makes it unbounded on
// purpose), so a leak checker would report the feature under test.
package runner_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// outageGateSeverance is how long the Controller's link is cut. It is the
// figure internal/event's reconnect gate uses, for the same reason: it is
// longer than the two minute producer dedup window this phase replaced, so
// the old window could not have covered it.
const outageGateSeverance = 150 * time.Second

// logRecorder is an slog.Handler that keeps every message, so the gate can
// see which mechanism suppressed a duplicate: the broker's producer-side
// window logs one line, the Runner's admission check another.
type logRecorder struct {
	mu       sync.Mutex
	messages []string
}

// Enabled accepts every level, since the lines the gate reads are Warn and
// Info.
func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

// Handle keeps the message and its attributes as one line.
func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	var b strings.Builder
	b.WriteString(rec.Message)
	rec.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	r.mu.Lock()
	r.messages = append(r.messages, b.String())
	r.mu.Unlock()
	return nil
}

// WithAttrs and WithGroup return the same recorder: the gate matches on
// messages, and the loggers it builds add no attributes.
func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

// WithGroup returns the same recorder, for the reason WithAttrs does.
func (r *logRecorder) WithGroup(string) slog.Handler { return r }

// count reports how many kept lines contain every one of parts.
func (r *logRecorder) count(parts ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.messages {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(m, p)
		}
		if all {
			n++
		}
	}
	return n
}

// TestAgent_DeliversADispatchExactlyOnceAcrossAnOutage is D3 inverted.
//
// PRE-FIX MEASUREMENT (D3, 2026-08-22): a JetStream publish made while the
// link was cut blocked for its whole context and returned "context
// deadline exceeded", yet the message was stored once the link healed. A
// caller cannot tell "did not happen" from "happened, but the ack was
// lost", so a retry is the only safe move, and the retry that matters most
// is the Controller's stale-job reclaim, which republishes minutes later.
// The broker's producer-side window cannot cover that retry: the window
// may not exceed half the reclaim interval, or it would swallow the
// reclaim itself. What covers it is the Runner's own admission check, a
// key in a NATS KV bucket written once the work has run.
//
// The gate runs the whole sequence against the real pieces, wired as
// cmd/runner wires them: a publisher through a Toxiproxy the test cuts
// (the Controller's link), and a Runner on a direct connection with the
// dedup bucket bound in the Runner's own role. It asserts, in order:
//
//  1. the publish made while severed reports failure (D3's false negative);
//  2. once the link heals, that publish is executed, once;
//  3. a retry right away is suppressed by the broker's window, which stores
//     nothing;
//  4. a retry after that window has passed IS stored by the broker, so the
//     broker is no longer what protects it, and the Runner's check skips it;
//  5. across all of it, the work ran exactly once.
//
// The outage budget is three minutes: legal (ParseOutageBudget accepts one
// minute to twelve hours), at least as long as the outage, so the run stays
// inside what the platform promises, and short enough that step 4's wait
// for the window to pass keeps the gate near six minutes.
func TestAgent_DeliversADispatchExactlyOnceAcrossAnOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the six minute outage release gate in short mode")
	}
	ctx := context.Background()
	budget := topology.OutageBudget(3 * time.Minute)
	window := topology.DerivedDuplicateWindow(budget)

	broker, proxiedURL, proxy := testsupport.NATSThroughToxiproxy(t)

	// The Controller's side: the stream is provisioned from the budget, so
	// its producer window is the derived one, and every publish crosses the
	// proxy this test cuts.
	busLogs := &logRecorder{}
	bus, err := event.NewNatsBus(ctx, proxiedURL, slog.New(busLogs), topology.StreamProvisioner, budget, false)
	if err != nil {
		t.Fatalf("nats bus through the proxy: %v", err)
	}
	// A defer, not t.Cleanup: defers run first, so the client closes before
	// the containers the harness removes in its cleanups.
	defer bus.Close()

	// The Runner's side, wired as cmd/runner/main.go wires it: its own
	// connection, the dispatch consumer, and the dedup bucket bound in the
	// Runner's role, which creates it when it is missing.
	runnerLogs := &logRecorder{}
	runnerLogger := slog.New(runnerLogs)
	nc, err := topology.Connect(ctx, broker.URL(), runnerLogger, "runner-dispatch")
	if err != nil {
		t.Fatalf("runner connection: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		t.Fatalf("dispatch consumer: %v", err)
	}
	dedupKV, err := topology.BindDedupBucket(ctx, js, topology.StreamReader)
	if err != nil {
		t.Fatalf("dedup bucket: %v", err)
	}
	adapter := newCountingAdapter()
	agent := runner.NewAgent(consumer, adapter, js, lock.NewInProcessManager(), topology.MaxDeliverDefault,
		runnerLogger, nil, runner.WithPoolSize(1),
		runner.WithDedupStore(event.NewNatsDedupStore(dedupKV), topology.DerivedDedupTTLFloor(budget)))
	agentCtx, cancelAgent := context.WithCancel(ctx)
	defer cancelAgent()
	go agent.Run(agentCtx)

	// One unit of work, published the way internal/dispatch's worker
	// publishes it: a fresh event id every time, and the same idempotency
	// key, jobID:deviceID, which is what both mechanisms recognise.
	jobID := uuid.New().String()
	const deviceID = "outage-gate-device"
	key := jobID + ":" + deviceID
	publish := func(ctx context.Context) error {
		evt, err := event.WrapPayload(uuid.New().String(), "runbook.dispatched", wire.DispatchPayload{
			JobID:         jobID,
			RunbookID:     "rb-outage",
			DeviceID:      deviceID,
			DeviceName:    "outage-device",
			DeviceHost:    "10.0.0.1",
			Interruptible: true,
		})
		if err != nil {
			t.Fatalf("wrap payload: %v", err)
		}
		return bus.Publish(event.WithIdempotencyKey(ctx, key), topology.DispatchSubject(deviceID), *evt)
	}
	stored := func() uint64 {
		stream, err := js.Stream(ctx, topology.StreamName)
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		info, err := stream.Info(ctx)
		if err != nil {
			t.Fatalf("stream info: %v", err)
		}
		return info.State.Msgs
	}

	// Step 1: cut the link and publish. The publish must report failure,
	// which is the false negative the rest of the gate has to survive.
	if err := proxy.Disable(); err != nil {
		t.Fatalf("disable proxy: %v", err)
	}
	severedAt := time.Now()
	severedCtx, cancelSevered := context.WithTimeout(ctx, 5*time.Second)
	err = publish(severedCtx)
	cancelSevered()
	if err == nil {
		t.Fatal("a publish across the cut link reported success; D3's false negative did not happen, so this run proves nothing")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("the severed publish failed with %v rather than a deadline", err)
	}

	time.Sleep(time.Until(severedAt.Add(outageGateSeverance)))
	if err := proxy.Enable(); err != nil {
		t.Fatalf("enable proxy: %v", err)
	}
	t.Logf("link cut for %v, then healed", time.Since(severedAt).Round(time.Second))

	// Step 2: the buffered publish reaches the broker when the client
	// reconnects, and the Runner executes it.
	waitFor := func(what string, limit time.Duration, done func() bool) {
		deadline := time.Now().Add(limit)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out after %v waiting for %s (executions: %v)", limit, what, adapter.snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitFor("the publish made while severed to execute", 90*time.Second, func() bool { return adapter.total() >= 1 })
	firstRun := time.Now()
	t.Logf("the publish made while severed executed %v after the heal", firstRun.Sub(severedAt.Add(outageGateSeverance)).Round(time.Second))

	// Step 3: the caller saw a failure and retries at once. The broker's
	// window, which began when the buffered publish was stored, suppresses
	// it and stores nothing.
	before := stored()
	if err := publish(ctx); err != nil {
		t.Fatalf("retry after the heal: %v", err)
	}
	if got := stored(); got != before {
		t.Errorf("the broker stored the immediate retry (%d messages, was %d); its window should have suppressed it", got, before)
	}
	if busLogs.count("publish suppressed as a duplicate", key) != 1 {
		t.Errorf("no broker-side suppression was logged for the immediate retry")
	}

	// Step 4: the retry the window cannot cover, the shape of the
	// Controller's stale-job reclaim. Retried until the broker stores it,
	// because the broker expires window entries on its own schedule: the
	// point is that once it has stored a second copy, only the Runner's
	// check stands between that copy and a second execution.
	time.Sleep(time.Until(firstRun.Add(window)))
	storedAgain := false
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Second) {
		before := stored()
		if err := publish(ctx); err != nil {
			t.Fatalf("retry after the window: %v", err)
		}
		if stored() > before {
			storedAgain = true
			break
		}
	}
	if !storedAgain {
		t.Fatalf("the broker kept suppressing the retry more than 90s past its %v window, so the Runner's check was never reached", window)
	}
	waitFor("the Runner to skip the stored duplicate", 60*time.Second, func() bool {
		return runnerLogs.count("skipping a dispatch that already executed") >= 1
	})

	// Step 5: exactly once.
	if got := adapter.total(); got != 1 {
		t.Fatalf("the dispatch executed %d times, want exactly 1 (executions: %v)", got, adapter.snapshot())
	}
}
