package topology_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startNats starts a real nats-server and returns its client URL.
func startNats(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	natsC, err := tcnats.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	t.Cleanup(func() { natsC.Terminate(context.Background()) })

	url, err := natsC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}

// TestConnectReturnsAUsableConnection proves the whole point of Connect:
// that what it hands back is connected, not merely allocated.
//
// The distinction is the reason this function exists. DialOptions sets
// RetryOnFailedConnect, so a bare nats.Connect returns before connecting,
// and every caller in this module immediately issues a server request
// against the handle it gets. A test that only checked for a nil error
// would pass against a handle that is still dialling.
func TestConnectReturnsAUsableConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()

	nc, err := topology.Connect(ctx, startNats(t), slog.Default(), "unit")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()

	if !nc.IsConnected() {
		t.Fatal("Connect returned a handle that is not connected, which is the exact trap RetryOnFailedConnect sets")
	}
	// A round trip, not a status field: the server has to answer.
	if err := nc.Flush(); err != nil {
		t.Fatalf("the returned connection could not reach the server: %v", err)
	}
	if name := nc.Opts.Name; name != "pleiades-unit" {
		t.Errorf("connection name = %q, want %q", name, "pleiades-unit")
	}
}

// TestConnectClosesTheConnectionWhenTheWaitFails proves Connect owns its
// cleanup, so a caller that receives an error owns nothing.
//
// This is the generalisation of FAILURE_PATTERNS.md #193, where two
// constructors leaked a live connection on every post-dial error path.
// Pairing the dial and the wait in one function is what makes that
// unrepresentable, and this asserts the pairing rather than trusting it.
func TestConnectClosesTheConnectionWhenTheWaitFails(t *testing.T) {
	// RFC 5737 TEST-NET-1: unroutable, so the wait always times out.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	nc, err := topology.Connect(ctx, "nats://192.0.2.1:4222", nil, "unit")
	if err == nil {
		nc.Close()
		t.Fatal("Connect succeeded against an unroutable address")
	}
	if nc != nil {
		t.Fatal("Connect returned both an error and a connection; a caller that gets an error must own nothing")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

// TestWaitForConnectReturnsOnAnAlreadyConnectedHandle covers the
// pre-check that exists for the registration race: a connection that
// reached CONNECTED before WaitForConnect was called must return at once
// rather than blocking for a notification that already happened.
func TestWaitForConnectReturnsOnAnAlreadyConnectedHandle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()

	nc, err := topology.Connect(ctx, startNats(t), nil, "unit")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()

	// Already connected by construction. A tight deadline proves the
	// pre-check returns rather than the select waiting one out.
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := topology.WaitForConnect(waitCtx, nc); err != nil {
		t.Fatalf("WaitForConnect on a connected handle: %v", err)
	}
}

// TestWaitForConnectReportsAClosedConnection covers both CLOSED arms and,
// with them, closedBeforeConnect's nil-LastError branch.
//
// That branch is not hypothetical: nats.go's close() never assigns an
// error, so an explicit Close of a still-connecting handle leaves
// LastError nil. Formatting a nil error with %w yields the literal text
// "%!w(<nil>)" and an error with no unwrap chain, which is what this
// asserts is no longer produced.
func TestWaitForConnectReportsAClosedConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()

	nc, err := topology.Connect(ctx, startNats(t), nil, "unit")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	nc.Close()

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	err = topology.WaitForConnect(waitCtx, nc)
	if err == nil {
		t.Fatal("WaitForConnect returned nil for a closed connection")
	}
	if !strings.Contains(err.Error(), "closed before it connected") {
		t.Errorf("error = %v, want it to name the closed-before-connected case", err)
	}
	if strings.Contains(err.Error(), "%!w") {
		t.Errorf("error = %v, want no malformed verb: fmt.Errorf(%%w) with a nil error renders as %%!w(<nil>)", err)
	}
}

// TestLifecycleHandlersRunAgainstARealConnection executes every handler
// body DialOptions installs, against a real *nats.Conn.
//
// Asserting the callbacks are non-nil is a tautology about a struct, and
// two real defects shipped inside these bodies while four such assertions
// passed (LESSONS_LEARNED.md #161). The bodies call real methods on the
// connection (ConnectedUrlRedacted, Stats, LastError), so running them
// against a real handle is what proves they neither panic nor read a
// field that does not exist. What they WRITE across a real severance is
// asserted by internal/event's release gate, which can sever a link;
// this package has no proxy in front of its broker.
func TestLifecycleHandlersRunAgainstARealConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()

	nc, err := topology.Connect(ctx, startNats(t), nil, "unit")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()

	opts := nats.GetDefaultOptions()
	for _, opt := range topology.DialOptions(slog.Default(), "unit") {
		if err := opt(&opts); err != nil {
			t.Fatalf("applying a dial option: %v", err)
		}
	}

	// Each of these panics if a handler reads something a real connection
	// does not have, which is the failure a non-nil check cannot see.
	opts.ConnectedCB(nc)
	opts.ReconnectedCB(nc)
	opts.DisconnectedErrCB(nc, errors.New("probe"))
	opts.ReconnectErrCB(nc, errors.New("probe"))
	opts.ClosedCB(nc)
	opts.AsyncErrorCB(nc, nil, errors.New("probe"))

	// The async error handler's other arm: a real subscription rather
	// than the nil one above.
	sub, err := nc.SubscribeSync("pleiades.unit.probe")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	opts.AsyncErrorCB(nc, sub, errors.New("probe"))
}

// TestConnectSurfacesADialError covers the branch where nats.Connect
// itself fails rather than deferring to the background reconnect loop.
//
// That branch is narrow now and worth naming, because it is easy to
// believe it is wider. RetryOnFailedConnect means an unreachable or
// unresolvable host does NOT error here: nats.Connect returns nil and
// retries in the background, which is why a malformed-looking address is
// no longer a way to reach this code. What still fails immediately is a
// URL the client rejects before it ever builds a server pool, such as one
// that mixes websocket and non-websocket schemes.
func TestConnectSurfacesADialError(t *testing.T) {
	nc, err := topology.Connect(context.Background(), "ws://h:1,nats://h:2", nil, "unit")
	if err == nil {
		nc.Close()
		t.Fatal("Connect accepted a URL mixing websocket and non-websocket schemes")
	}
	if nc != nil {
		t.Fatal("Connect returned both an error and a connection")
	}
	if !strings.Contains(err.Error(), "websocket") {
		t.Errorf("error = %v, want the driver's own rejection", err)
	}
}

// TestWaitForConnectObservesTheStatusChange covers the arm where the
// connection is genuinely not connected when the wait begins and becomes
// connected while it is waiting, which is the case the whole function
// exists for and the one Connect's own callers always take.
//
// It dials with nats.Connect directly rather than through Connect,
// because Connect has already waited by the time it returns. That is the
// one legitimate reason to call the driver directly inside this package,
// and internal/archtest's TestOnlyTopologyDialsNats permits it here and
// nowhere else.
func TestWaitForConnectObservesTheStatusChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	url := startNats(t)

	nc, err := nats.Connect(url, topology.DialOptions(nil, "unit")...)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer nc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), topology.ConnectWaitTimeout)
	defer cancel()
	if err := topology.WaitForConnect(ctx, nc); err != nil {
		t.Fatalf("WaitForConnect: %v", err)
	}
	if !nc.IsConnected() {
		t.Fatal("WaitForConnect returned nil for a connection that is not connected")
	}
}

// TestConnectStateReadsARealConnection drives every arm of the decision
// WaitForConnect makes, against real connections rather than a fake.
//
// The arms live in a function precisely so they can be reached this way:
// the same three-way check inside a select is only reachable by winning a
// race, which is how the previous shape ended up with four untested
// branches in the code path that decides whether a process starts.
func TestConnectStateReadsARealConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	ctx := context.Background()

	t.Run("connected", func(t *testing.T) {
		nc, err := topology.Connect(ctx, startNats(t), nil, "unit")
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
		defer nc.Close()

		done, err := topology.ConnectStateForTest(nc)
		if !done || err != nil {
			t.Fatalf("connectState on a live connection = (%v, %v), want (true, nil)", done, err)
		}
	})

	t.Run("closed", func(t *testing.T) {
		nc, err := topology.Connect(ctx, startNats(t), nil, "unit")
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
		nc.Close()

		done, err := topology.ConnectStateForTest(nc)
		if !done {
			t.Fatal("connectState on a closed connection reported not done")
		}
		if err == nil || !strings.Contains(err.Error(), "closed before it connected") {
			t.Fatalf("connectState error = %v, want the closed-before-connected case", err)
		}
	})

	t.Run("still trying", func(t *testing.T) {
		// Unroutable, so it stays in the reconnect loop indefinitely.
		nc, err := nats.Connect("nats://192.0.2.1:4222", topology.DialOptions(nil, "unit")...)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer nc.Close()

		done, err := topology.ConnectStateForTest(nc)
		if done || err != nil {
			t.Fatalf("connectState on a connecting connection = (%v, %v), want (false, nil)", done, err)
		}
	})
}
