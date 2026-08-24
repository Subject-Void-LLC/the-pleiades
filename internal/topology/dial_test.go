package topology_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
)

// TestDialOptionsAppliesTheReconnectSettingsThatCloseD1 asserts the two
// options whose absence was the measured defect, by applying the real
// option set to a real nats.Options the way nats.Connect does, rather
// than by reading the slice's length.
//
// MaxReconnects(-1) is the one that matters most: at the nats.go default
// of 60 attempts two seconds apart, a link outage of 2m3s exhausted the
// budget and closed the connection permanently, which a 30-second watch
// of a fully healed network then confirmed it never recovered from.
func TestDialOptionsAppliesTheReconnectSettingsThatCloseD1(t *testing.T) {
	opts := applyDialOptions(t, "unit")

	if opts.MaxReconnect != -1 {
		t.Errorf("MaxReconnect = %d, want -1 (unlimited); at any finite budget a long enough outage closes the connection for good, which is Phase 96a's D1", opts.MaxReconnect)
	}
	if !opts.RetryOnFailedConnect {
		t.Error("RetryOnFailedConnect = false, want true; without it a dial against a broker that has not started yet returns an error every composition root treats as fatal, which is Phase 96a's D2")
	}
	if opts.Timeout != topology.DialTimeout {
		t.Errorf("Timeout = %v, want %v", opts.Timeout, topology.DialTimeout)
	}
	if opts.PingInterval != topology.PingInterval {
		t.Errorf("PingInterval = %v, want %v", opts.PingInterval, topology.PingInterval)
	}
	if opts.MaxPingsOut != topology.MaxPingsOutstanding {
		t.Errorf("MaxPingsOut = %d, want %d", opts.MaxPingsOut, topology.MaxPingsOutstanding)
	}

	// The detection window is the number the constants exist to produce,
	// and it is off by one from the obvious reading: nats.go declares a
	// stale connection when pout EXCEEDS MaxPingsOut, so the failure
	// lands on tick MaxPingsOut+1. Asserting the product rather than the
	// factors is what catches a future edit that changes one of them and
	// leaves the doc comment claiming the old window.
	const wantBlackHoleDetection = 60 * time.Second
	if got := topology.PingInterval * time.Duration(topology.MaxPingsOutstanding+1); got != wantBlackHoleDetection {
		t.Errorf("black-hole detection window = %v, want %v; PingInterval x (MaxPingsOutstanding+1) is what nats.go actually waits before declaring a stale connection", got, wantBlackHoleDetection)
	}
}

// TestDialOptionsRegistersEveryLifecycleHandler asserts the Observer half.
// Before Phase 96a not one of the five dial sites set any of these, so a
// mesh connection could die without producing a single log line, which is
// how a Runner could look healthy while doing no work at all.
func TestDialOptionsRegistersEveryLifecycleHandler(t *testing.T) {
	opts := applyDialOptions(t, "unit")

	if opts.DisconnectedErrCB == nil {
		t.Error("DisconnectErrHandler not set: a lost connection would be silent")
	}
	if opts.ReconnectedCB == nil {
		t.Error("ReconnectHandler not set: a recovery would be silent")
	}
	if opts.ClosedCB == nil {
		t.Error("ClosedHandler not set: a permanently closed connection would be silent")
	}
	if opts.AsyncErrorCB == nil {
		t.Error("ErrorHandler not set: asynchronous errors would be silent")
	}
	if opts.CustomReconnectDelayCB == nil {
		t.Error("CustomReconnectDelay not set: reconnects would use the fixed 2s ReconnectWait instead of pkg/retry.Backoff")
	}

	// The initial-connect retry window routes through a DIFFERENT pair of
	// callbacks than the steady state: while Conn.initc is true, nats.go
	// consults ReconnectErrCB and never DisconnectedErrCB, and the
	// eventual first success calls ConnectedCB and never ReconnectedCB.
	// Registering only the steady-state pair left the entire cold-start
	// path that RetryOnFailedConnect exists to enable completely silent.
	if opts.ConnectedCB == nil {
		t.Error("ConnectHandler not set: the first successful connect after a retry would be silent, because nats.go skips ReconnectedCB while initc is true")
	}
	if opts.ReconnectErrCB == nil {
		t.Error("ReconnectErrHandler not set: a failing cold-start retry would be silent, because nats.go skips DisconnectedErrCB while initc is true")
	}

	// Without this, Conn.Close() invokes the disconnect and closed
	// handlers, so every deliberate shutdown logged a WARN about
	// reconnecting and an ERROR about a permanent close.
	if !opts.NoCallbacksAfterClientClose {
		t.Error("NoCallbacksAfterClientClose not set: a graceful Close would emit a false disconnect WARN and a false permanent-close ERROR, six per pod on a rolling update")
	}
}

// TestDialOptionsNamesTheConnection proves the component argument reaches
// the server-visible connection name, which is the only way an operator
// reading a connection list can tell a Runner's three connections apart.
func TestDialOptionsNamesTheConnection(t *testing.T) {
	opts := applyDialOptions(t, "runner-dispatch")
	const want = "pleiades-runner-dispatch"
	if opts.Name != want {
		t.Errorf("Name = %q, want %q", opts.Name, want)
	}
}

// TestDialOptionsToleratesANilLogger covers the cmd/demo case, which is
// the one composition root with no slog.Logger of its own to pass.
func TestDialOptionsToleratesANilLogger(t *testing.T) {
	opts := applyDialOptions(t, "demo")
	if opts.DisconnectedErrCB == nil {
		t.Fatal("a nil logger produced no handlers; it should fall back to slog.Default()")
	}
	// What the handlers actually WRITE is asserted against a real
	// severance, with a real logger captured, by
	// internal/event.TestNatsBus_LogsTheConnectionLifecycle. Asserting a
	// function pointer is non-nil is worth exactly what it sounds like:
	// it is how two real defects in these handlers survived review.
}

// TestReconnectDelayGrowsAndIsBounded pins the two properties nats.go
// actually depends on, against the constants rather than against
// hardcoded durations.
func TestReconnectDelayGrowsAndIsBounded(t *testing.T) {
	// nats.go passes a 1-based attempt count. Attempt 1 is the first
	// retry and must not already be at the cap, which is what passing
	// nats.go's value straight into a 0-based exponent would cause.
	first := topology.ReconnectDelay(1)
	if first < topology.ReconnectBaseDelay {
		t.Errorf("ReconnectDelay(1) = %v, want at least the base %v", first, topology.ReconnectBaseDelay)
	}
	if first > topology.ReconnectBaseDelay*2 {
		t.Errorf("ReconnectDelay(1) = %v, want no more than one base step plus jitter; a first retry already at the ceiling means the attempt count was not rebased", first)
	}

	if grown := topology.ReconnectDelay(5); grown <= first {
		t.Errorf("ReconnectDelay(5) = %v, want more than ReconnectDelay(1) = %v", grown, first)
	}
	if capped := topology.ReconnectDelay(1000); capped != topology.ReconnectMaxDelay {
		t.Errorf("ReconnectDelay(1000) = %v, want the cap %v", capped, topology.ReconnectMaxDelay)
	}
}

// FuzzReconnectDelay is the phase's fuzz target. The function is called
// by nats.go once per reconnect attempt with a counter this code does not
// control, and a zero or negative return would spin the reconnect loop
// against a dead link as fast as the CPU allows, while an unbounded one
// would strand a Runner. Both properties must hold for every int,
// including the negatives and the overflow-adjacent values a counter can
// reach after a long enough outage.
func FuzzReconnectDelay(f *testing.F) {
	for _, seed := range []int{-1 << 62, -1, 0, 1, 2, 62, 63, 64, 1 << 31, 1<<63 - 1} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, attempt int) {
		d := topology.ReconnectDelay(attempt)
		if d <= 0 {
			t.Fatalf("ReconnectDelay(%d) = %v, want a strictly positive delay; a zero or negative one busy-loops the reconnect attempt", attempt, d)
		}
		if d > topology.ReconnectMaxDelay {
			t.Fatalf("ReconnectDelay(%d) = %v, want no more than %v", attempt, d, topology.ReconnectMaxDelay)
		}
	})
}

// TestWaitForConnectGivesUpWhenTheBrokerNeverAppears exercises the real
// path against a real dial, not a fabricated *nats.Conn.
//
// The address is RFC 5737 TEST-NET-1, which is guaranteed unroutable and
// is the exact shape Phase 96a's D2 measured as failing in 2s with
// "dial tcp: i/o timeout". With RetryOnFailedConnect set, that dial no
// longer returns an error: nats.Connect hands back a connection that is
// working on it, which is precisely why WaitForConnect has to exist. The
// assertion is that the wait ends on the caller's context rather than
// hanging, and that the returned error says so.
func TestWaitForConnectGivesUpWhenTheBrokerNeverAppears(t *testing.T) {
	nc, err := nats.Connect("nats://192.0.2.1:4222", topology.DialOptions(nil, "unit")...)
	if err != nil {
		t.Fatalf("Connect returned an error against an unroutable address; RetryOnFailedConnect should have made it return a reconnecting handle instead: %v", err)
	}
	defer nc.Close()

	if nc.IsConnected() {
		t.Fatal("connected to an RFC 5737 TEST-NET-1 address, which should be unroutable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = topology.WaitForConnect(ctx, nc)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("WaitForConnect returned nil against an unroutable broker")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitForConnect error = %v, want it to wrap context.DeadlineExceeded so a caller can tell a timeout from a refusal", err)
	}
	// Generous, because this asserts "bounded" rather than "prompt": the
	// point is that it returns on the context instead of blocking on an
	// unlimited reconnect loop, which is what MaxReconnects(-1) would
	// otherwise mean for a startup path.
	if elapsed > 5*time.Second {
		t.Errorf("WaitForConnect took %v against a 250ms context; it is not honouring the caller's deadline", elapsed)
	}
}

// applyDialOptions runs the real option set through nats.Options the same
// way nats.Connect does, so these tests assert what the driver will
// actually see rather than what the slice looks like.
func applyDialOptions(t *testing.T, component string) nats.Options {
	t.Helper()

	opts := nats.GetDefaultOptions()
	var logger *slog.Logger
	if component != "demo" {
		logger = slog.Default()
	}
	for _, opt := range topology.DialOptions(logger, component) {
		if err := opt(&opts); err != nil {
			t.Fatalf("applying a dial option: %v", err)
		}
	}
	return opts
}
