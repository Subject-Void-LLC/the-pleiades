// Phase 101c's revocation measurement, which the phase calls its own
// honest weakness and asks to be specced rather than glossed.
//
// # What revocation actually is here, and why it is awkward
//
// A minted credential is a bearer token that the broker accepts until it
// expires, by checking a signature chain and nothing else. There is no
// lookup, which is exactly the property that makes minting a Smart Hands
// credential need no broker configuration change. Early revocation has to
// reintroduce the lookup, and NATS does it by putting a revocation list
// inside the ACCOUNT JWT.
//
// Three consequences follow, and all three are constraints on the design
// rather than details of it:
//
//  1. A revocation entry keys on the user's PUBLIC KEY and its value is a
//     UNIX seconds watermark: a credential is revoked when the stored
//     timestamp is at or after the credential's IssuedAt. It is not
//     "revoke this credential", it is "every credential for this key
//     issued at or before this second". Coverage only ever widens.
//
//  2. Publishing it means re-encoding the account JWT, which must be
//     signed by the account identity key or the operator key. Both are
//     the keys this platform deliberately keeps offline. So revocation is
//     an OFFLINE operation by construction, not a control plane one, and
//     no amount of engineering inside the Controller changes that without
//     moving a key the whole design exists to keep out of it.
//
//  3. The re-encoded JWT has to reach the resolver. With the memory
//     resolver this deployment uses, that means the server's own
//     configuration and a reload.
//
// So the live mechanism is expiry, and revocation is the break-glass
// path. What this file measures is the part that was only ever assumed:
// what a revocation does to a client that is ALREADY CONNECTED.
package meshid_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/testcontainers/testcontainers-go"
)

// TestReleaseGate_RevokingAUserEvictsItsLiveConnection is the
// measurement, against a real server, of the thing the phase says must
// not be assumed.
func TestReleaseGate_RevokingAUserEvictsItsLiveConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The operator is minted normally and then its key is recovered from
	// its seed, which is exactly the ceremony a real revocation requires:
	// somebody fetches the operator key from wherever it was stored
	// offline and signs with it. A Controller cannot do this, by design,
	// and that is the finding rather than a limitation of the test.
	op, err := meshid.NewOperator("revocation-op")
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

	opSeed, err := op.Seed()
	if err != nil {
		t.Fatalf("reading the operator seed: %v", err)
	}
	opKP, err := nkeys.FromSeed(opSeed)
	if err != nil {
		t.Fatalf("recovering the operator key from its seed: %v", err)
	}
	defer opKP.Wipe()

	broker := testsupport.StartNATS(t, testsupport.WithNATSConfig(
		revocationConfig(op.JWT, sys.Subject, sys.JWT, app.Subject, app.JWT)))
	url := broker.URL()

	issuer, err := meshid.NewIssuer(app.Subject, app.SigningKeySeed)
	if err != nil {
		t.Fatalf("building the issuer: %v", err)
	}
	// A long window, so that anything observed below is revocation and
	// not expiry. The expiry gate beside this one measures expiry.
	cred, err := issuer.Issue(meshid.FleetRunnerGrant("doomed-runner"), time.Hour)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	closed := make(chan struct{})
	conn, err := topology.Connect(ctx, url, logger, "doomed-runner", topology.WithCredentials(cred.Creds))
	if err != nil {
		t.Fatalf("connecting with a valid credential: %v", err)
	}
	defer conn.Close()
	conn.SetClosedHandler(func(_ *nats.Conn) { close(closed) })

	// ---- Act 1: the control. It works before the revocation. ----
	if err := conn.Publish(topology.LogSubject("revocation-1"), []byte("alive")); err != nil {
		t.Fatalf("publishing before the revocation: %v", err)
	}
	if err := conn.Flush(); err != nil {
		t.Fatalf("flushing before the revocation: %v", err)
	}

	// ---- Act 2: revoke, offline, and republish the account JWT. ----
	//
	// This is the whole ceremony, and its length is the finding. Decode
	// the account claims, add the revocation, re-encode with the OPERATOR
	// key, write a new server configuration, and reload the server.
	claims, err := jwt.DecodeAccountClaims(app.JWT)
	if err != nil {
		t.Fatalf("decoding the account claims: %v", err)
	}
	claims.Revoke(cred.Subject)
	revokedJWT, err := claims.Encode(opKP)
	if err != nil {
		t.Fatalf("re-encoding the account jwt with the revocation: %v", err)
	}

	newConf := revocationConfig(op.JWT, sys.Subject, sys.JWT, app.Subject, revokedJWT)
	if err := broker.Container.CopyToContainer(ctx, []byte(newConf), testsupport.NATSConfigPath, 0o644); err != nil {
		t.Fatalf("replacing the broker configuration: %v", err)
	}
	code, out, err := broker.Container.Exec(ctx, []string{"sh", "-c", "kill -HUP 1"})
	if err != nil {
		t.Fatalf("reloading the broker: %v", err)
	}
	if code != 0 {
		body, _ := io.ReadAll(out)
		t.Fatalf("the broker refused the reload signal (exit %d): %s", code, body)
	}

	// ---- Act 3: the measurement. ----
	//
	// The deadline is generous because this measures a real server's own
	// timing rather than asserting a number, exactly as the expiry gate
	// does. What is asserted is the durable end state, never the wire
	// text.
	// The control for this act was run as a falsification rather than
	// left in, because it costs the full 45 second deadline every time.
	// Removing the claims.Revoke line above and reloading the IDENTICAL
	// configuration leaves the connection open for the whole window. So
	// what closes it is the revocation and not the reload, which is the
	// one alternative explanation this assertion has.
	select {
	case <-closed:
		t.Log("MEASURED: a revoked user's live connection is closed by the broker after a configuration reload")
	case <-time.After(45 * time.Second):
		t.Fatal("a revoked user's connection was still open 45s after the account JWT carrying its revocation was loaded; revocation does not evict a live client, so a compromised credential keeps working until it expires and the operational answer is to shorten expiry rather than to revoke")
	}

	// And the credential must not be usable to reconnect either, which is
	// the half that matters for a stolen file.
	if again, err := topology.Connect(ctx, url, logger, "doomed-runner-again",
		topology.WithCredentials(cred.Creds)); err == nil {
		again.Close()
		t.Error("a revoked credential still connects; revocation is not enforced at connect time either")
	}
}

// revocationConfig renders an operator-mode configuration with one
// application account whose JWT the caller supplies, so the same shape
// can be rendered before and after a revocation.
func revocationConfig(opJWT, sysSubject, sysJWT, appSubject, appJWT string) string {
	return fmt.Sprintf(`
operator: %s
system_account: %s
resolver: MEMORY
resolver_preload: {
  %s: %s
  %s: %s
}
`, opJWT, sysSubject, sysSubject, sysJWT, appSubject, appJWT)
}

// ensure the testcontainers import is used for its Container type.
var _ testcontainers.Container
