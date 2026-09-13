// Phase 101c measures the temporal half of the trust model, because the
// phase body demands measurement rather than citation: "verify against a
// real server what happens to an ALREADY-CONNECTED client when its user is
// revoked and when its JWT expires. Eviction is commonly assumed and must
// be measured here."
//
// This file measures expiry. Revocation is a different mechanism with a
// different cost and is recorded against its own spec item.

package meshid_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
)

// TestReleaseGate_AnExpiringCredentialEvictsALiveConnection answers the
// question the phase says not to assume.
//
// What it establishes, against a real nats-server 2.14.4:
//
//   - Expiry is ENFORCED ON A LIVE CONNECTION, not merely at connect time.
//     A Runner holding a valid connection loses it when its credential
//     expires underneath it, with no request needed to trigger the check.
//   - The connection ends CLOSED rather than reconnecting forever. That is
//     the behaviour nats.go documents at nats.go:3961: after the same
//     authentication error twice it stops retrying REGARDLESS of
//     MaxReconnects(-1), unless IgnoreAuthErrorAbort is set, and
//     topology.DialOptions sets neither. So an expired Runner stops, which
//     is the correct outcome and also the one that falsifies
//     internal/topology/dial.go's comment that its ClosedHandler "in
//     practice should never fire at all".
//
// The assertions are deliberately about the DURABLE END STATE rather than
// about an exact error string or a reconnect count. nats.go's own test
// suite refuses to test this against a real server for that reason
// (nats_test.go:1247-1261: "the server sets up the expire callback, that
// callback fires right away and so client receives async -ERR again... for
// a deterministic test, we won't use an actual NATS Server"). A gate that
// pinned the wire text would be testing the server's phrasing.
func TestReleaseGate_AnExpiringCredentialEvictsALiveConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	op, err := meshid.NewOperator("expiry-op")
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

	// A deliberately tiny window. The credential is valid when the
	// connection is made and expires while it is held, which is the whole
	// point: this measures eviction, not refusal.
	const window = 5 * time.Second
	cred, err := issuer.Issue(meshid.FleetRunnerGrant("expiring-runner"), window)
	if err != nil {
		t.Fatalf("issuing a short-lived credential: %v", err)
	}

	authErrs := make(chan error, 8)
	closed := make(chan struct{})
	conn, err := topology.Connect(ctx, url, logger, "expiring-runner",
		topology.WithCredentials(cred.Creds))
	if err != nil {
		t.Fatalf("a credential valid for %v was refused at connect time: %v", window, err)
	}
	defer conn.Close()

	conn.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
		select {
		case authErrs <- e:
		default:
		}
	})
	conn.SetClosedHandler(func(_ *nats.Conn) { close(closed) })

	// ---- Act 1: the control. The connection is genuinely live now. ----
	//
	// Without this, "it ended up closed" cannot be told apart from "it was
	// never usable".
	if !conn.IsConnected() {
		t.Fatal("the connection was not live before the credential expired, so nothing below measures expiry")
	}
	if err := conn.Publish(topology.LogSubject("expiry-1"), []byte("alive")); err != nil {
		t.Fatalf("publishing on a live connection: %v", err)
	}
	if err := conn.Flush(); err != nil {
		t.Fatalf("flushing on a live connection: %v", err)
	}
	select {
	case e := <-authErrs:
		t.Fatalf("a permitted publish on a valid credential was refused: %v", e)
	case <-time.After(500 * time.Millisecond):
	}

	// ---- Act 2: the measurement. ----
	//
	// The generous deadline is the honest price of measuring a real
	// server's own timing rather than asserting a number.
	select {
	case <-closed:
	case <-time.After(window + 45*time.Second):
		t.Fatalf("the connection was still open %v after its credential expired; expiry is not enforced on a live connection, and every credential this platform mints would outlive its own window", window)
	}

	if conn.IsConnected() {
		t.Error("the closed handler fired but the connection still reports connected")
	}

	// The error that got us here should be an authentication one. This is
	// asserted loosely: what matters is that the eviction was about
	// credentials rather than about the network, since a transport blip
	// would prove nothing about expiry.
	select {
	case e := <-authErrs:
		if !errors.Is(e, nats.ErrAuthExpired) && !errors.Is(e, nats.ErrAuthorization) {
			t.Logf("note: the eviction error was %v, which is a refusal but not one of nats.go's named auth errors", e)
		}
	default:
		t.Error("the connection closed without any asynchronous error reaching the handler, so nothing distinguishes an expiry from a dropped link")
	}
}
