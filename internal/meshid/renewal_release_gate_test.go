// Phase 101c's counterpart to the expiry gate, and the reason
// topology.WithCredentialSource exists.
//
// Read the two together. The expiry gate proves that a connection holding
// a FIXED credential dies when that credential expires: the broker evicts
// it, and nats.go abandons reconnection after the same authentication
// error twice regardless of MaxReconnects(-1). That is correct behavior
// and it is also a problem, because a Runner is a long-lived process and
// every credential this platform mints has a window. Left there, every
// Runner in the fleet stops taking work exactly one window after it
// starts, and the failure looks like an idle Runner rather than an
// expired one.
//
// This gate proves the other half: a connection whose credential arrives
// from a SOURCE re-presents whatever that source currently holds, so a
// renewal that lands before the window closes keeps the connection
// working. Nothing here is asserted from the driver's documentation; it
// is measured against a real operator-mode broker, because the claim is
// about what the SERVER does with a re-presented JWT on a reconnect.
package meshid_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
)

// TestReleaseGate_ARenewedCredentialKeepsALiveConnectionWorking is the
// deliverable, and it is falsifiable in both directions by construction:
// the expiry gate beside it is the same scenario with the renewal removed,
// and it asserts the opposite outcome.
func TestReleaseGate_ARenewedCredentialKeepsALiveConnectionWorking(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	op, err := meshid.NewOperator("renewal-op")
	if err != nil {
		t.Fatalf("minting the operator: %v", err)
	}
	sys, err := meshid.NewSystemAccount(op, "SYS")
	if err != nil {
		t.Fatalf("minting the system account: %v", err)
	}
	app, err := meshid.NewAccount(op, "PLEIADES")
	if err != nil {
		t.Fatalf("minting the application account: %v", err)
	}
	url := startOperatorModeBroker(t, jetStreamOperatorModeConfig(op, sys, app))

	issuer, err := meshid.NewIssuer(app.Subject, app.SigningKeySeed)
	if err != nil {
		t.Fatalf("building the issuer: %v", err)
	}

	// The same tiny window the expiry gate uses, so the two gates differ
	// in exactly one thing: whether the credential is renewed.
	const window = 5 * time.Second

	// A renewing source. This stands in for what the Controller does with
	// the signing key it already holds, and for what a Runner does with
	// whatever its renewal most recently obtained. The mutex is not
	// decoration: nats.go calls the source from its own reconnect
	// goroutine, which is exactly the concurrency
	// WithCredentialSource's doc comment warns about.
	var (
		mu      sync.Mutex
		current []byte
		issued  int
	)
	renew := func() {
		cred, err := issuer.Issue(meshid.FleetRunnerGrant("renewing-runner"), window)
		if err != nil {
			t.Errorf("renewing the credential: %v", err)
			return
		}
		mu.Lock()
		current = cred.Creds
		issued++
		mu.Unlock()
	}
	renew()

	closed := make(chan struct{})
	conn, err := topology.Connect(ctx, url, logger, "renewing-runner",
		topology.WithCredentialSource(func() ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			return current, nil
		}))
	if err != nil {
		t.Fatalf("a freshly issued credential was refused at connect time: %v", err)
	}
	defer conn.Close()
	conn.SetClosedHandler(func(_ *nats.Conn) { close(closed) })

	// ---- Act 1: the control. The connection is genuinely live. ----
	if !conn.IsConnected() {
		t.Fatal("the connection was not live at the start, so nothing below measures renewal")
	}
	if err := conn.Publish(topology.LogSubject("renewal-1"), []byte("alive")); err != nil {
		t.Fatalf("publishing on a live connection: %v", err)
	}
	if err := conn.Flush(); err != nil {
		t.Fatalf("flushing on a live connection: %v", err)
	}

	// ---- Act 2: renew repeatedly across more than one whole window. ----
	//
	// The renewal runs on a timer well inside the window, which is what a
	// real renewal loop does. The connection is expected to survive; if
	// the broker evicts it anyway, the source is re-read on the reconnect
	// and it comes back, and either route is a pass so long as the
	// connection is usable at the end. What must NOT happen is the
	// terminal CLOSED state the expiry gate measures, because nats.go
	// only reaches that after refusing to retry.
	deadline := time.After(window * 3)
	ticker := time.NewTicker(window / 2)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-ticker.C:
			renew()
		case <-closed:
			t.Fatal("the connection reached CLOSED despite its credential being renewed, so renewal does not keep a long-lived process alive and every Runner would still stop one window after starting")
		case <-deadline:
			break loop
		}
	}

	// ---- Act 3: the deliverable. It still works. ----
	//
	// Asserted by doing real work rather than by reading IsConnected,
	// because a handle can report connected while the server has stopped
	// authorizing it. A flushed publish is a round trip the broker has to
	// have accepted.
	if err := conn.Publish(topology.LogSubject("renewal-2"), []byte("still alive")); err != nil {
		t.Fatalf("publishing after %v of renewals: %v", window*3, err)
	}
	if err := conn.Flush(); err != nil {
		t.Fatalf("flushing after %v of renewals: %v; the credential was renewed but the connection did not survive", window*3, err)
	}

	// The positive control on the renewal itself: if the source had
	// handed back the same bytes forever, this gate would be the expiry
	// gate with a longer timeout and would prove nothing about renewal.
	mu.Lock()
	n := issued
	mu.Unlock()
	if n < 2 {
		t.Fatalf("only %d credential(s) were ever issued, so nothing was renewed and this gate proved nothing", n)
	}
}
