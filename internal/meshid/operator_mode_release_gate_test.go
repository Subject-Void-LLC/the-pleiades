// Phase 101b's Release Gate: a real nats-server running in operator mode
// accepts a credential this package minted, refuses one it did not, and
// enforces the permissions the grant declared.
//
// The act that matters most is the third. Phase 101's own body says that in
// operator mode user JWTs are never pushed to the server, that only account
// JWTs live in the resolver, and that the server therefore validates an
// unknown user by signature chain alone; and it says to PROVE that against
// a real server rather than cite it, because the whole deployability of a
// Smart Hands credential rests on it. If it holds, minting a credential for
// a technician at a remote site needs no broker configuration change at
// all. Act three mints a credential AFTER the server is running and
// connects with it, having touched nothing on the server.

package meshid_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
)

// operatorModeConfig renders the server configuration that makes a broker
// trust an operator and resolve its accounts.
//
// resolver: MEMORY with preloaded accounts is the shape this proves.
// Nothing about a USER appears here, which is the property act three
// depends on.
//
// The system account is not optional and was not always here. This gate
// used to start its broker with "-c" alone, replacing the flag list
// instead of adding to it, which made it the only NATS container in this
// repository running without JetStream. Phase 101c gave every test broker
// the deployment's own flags, and this configuration then stopped the
// server dead at boot. Measured rather than reasoned about, against
// nats-server 2.14.4 on 2026-09-23:
//
//	[WRN] Trusted Operators should utilize a System Account
//	[FTL] Can't start JetStream: setting up internal jetstream
//	      subscriptions failed: system account not setup
//
// So an operator-mode deployment of this platform needs TWO accounts
// minted, and the system account must have JetStream disabled, which is
// what meshid.NewSystemAccount exists to say at the call site.
func operatorModeConfig(op *meshid.Operator, sys, acct *meshid.Account) string {
	return fmt.Sprintf(`
operator: %s
system_account: %s
resolver: MEMORY
resolver_preload: {
  %s: %s
  %s: %s
}
`, op.JWT, sys.Subject, sys.Subject, sys.JWT, acct.Subject, acct.JWT)
}

// startOperatorModeBroker brings up a real broker with that configuration.
//
// Operator mode is only expressible as a configuration FILE, never as
// flags, which is why this gate needs one at all. Everything about HOW a
// broker is started now lives in testsupport.StartNATS, including the
// rule that the file is added to the deployment's flag list rather than
// replacing it. That rule is the reason this gate stopped being the one
// broker in the repository running without JetStream.
func startOperatorModeBroker(t *testing.T, conf string) string {
	t.Helper()
	return testsupport.StartNATS(t, testsupport.WithNATSConfig(conf)).URL()
}

// dialWith connects through the REAL dial path, topology.Connect with
// topology.WithCredentials, rather than calling nats.Connect directly.
//
// That is RULE 0 applied to this gate: the thing being proven is that a
// Runner can authenticate, and a Runner reaches the broker through exactly
// this function. A test that assembled its own nats.Option would prove the
// library works and leave the option this platform actually passes
// unexercised.
//
// A refusal costs ConnectWaitTimeout rather than failing instantly,
// because DialOptions sets RetryOnFailedConnect with unlimited reconnects
// so a Runner survives a broker that is not up yet. The negative acts
// below therefore take about ten seconds each, which is the honest price
// of testing the real path.
func dialWith(t *testing.T, url string, creds []byte) (*nats.Conn, error) {
	t.Helper()
	return topology.Connect(context.Background(), url,
		slog.New(slog.NewTextHandler(io.Discard, nil)), "release-gate",
		topology.WithCredentials(creds))
}

func TestReleaseGate_ARealBrokerAcceptsAMintedIdentityAndEnforcesItsGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	op, err := meshid.NewOperator("pleiades-test")
	if err != nil {
		t.Fatalf("NewOperator: %v", err)
	}
	sys, err := meshid.NewSystemAccount(op, "SYS")
	if err != nil {
		t.Fatalf("NewSystemAccount: %v", err)
	}
	acct, err := meshid.NewAccount(op, "pleiades")
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	issuer, err := meshid.NewIssuer(acct.Subject, acct.SigningKeySeed)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	url := startOperatorModeBroker(t, operatorModeConfig(op, sys, acct))

	// ---- Act one: an anonymous client is refused. ----
	//
	// Without this the rest proves only that a broker accepts
	// connections, which it would do with no authentication at all.
	if nc, err := topology.Connect(context.Background(), url,
		slog.New(slog.NewTextHandler(io.Discard, nil)), "release-gate-anon"); err == nil {
		nc.Close()
		t.Fatal("the broker accepted an anonymous connection, so it is not actually in operator mode and nothing below means anything")
	}

	// ---- Act two: a minted credential connects. ----
	cred, err := issuer.Issue(meshid.FleetRunnerGrant("runner-1"), time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	nc, err := dialWith(t, url, cred.Creds)
	if err != nil {
		t.Fatalf("a credential minted by this account was refused: %v", err)
	}
	defer nc.Close()

	// ---- Act three: zero broker configuration change. ----
	//
	// This credential is minted AFTER the server started and the server is
	// not told about it in any way. If it connects, then user JWTs really
	// are validated by signature chain alone and a Smart Hands credential
	// can be issued at the edge without touching the broker.
	later, err := issuer.Issue(meshid.FleetRunnerGrant("runner-minted-later"), time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	lnc, err := dialWith(t, url, later.Creds)
	if err != nil {
		t.Fatalf("a credential minted after the broker started was refused, so every issuance needs a broker change: %v", err)
	}
	lnc.Close()

	// ---- Act four: a stranger is refused. ----
	//
	// A different operator and account entirely, structurally identical
	// and signed by keys this broker has never heard of. Act two would
	// pass identically against a broker that accepted any well-formed JWT.
	strangerOp, err := meshid.NewOperator("stranger")
	if err != nil {
		t.Fatalf("NewOperator: %v", err)
	}
	strangerAcct, err := meshid.NewAccount(strangerOp, "stranger")
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	strangerIssuer, err := meshid.NewIssuer(strangerAcct.Subject, strangerAcct.SigningKeySeed)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	strangerCred, err := strangerIssuer.Issue(meshid.FleetRunnerGrant("intruder"), time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if snc, err := dialWith(t, url, strangerCred.Creds); err == nil {
		snc.Close()
		t.Error("a credential from an account this broker has never heard of was accepted")
	}

	// ---- Act five: the grant is enforced, both directions. ----
	//
	// A permission violation is not returned by Publish, which is fire and
	// forget. The server reports it asynchronously, so the error handler is
	// the only place it appears.
	violations := make(chan error, 4)
	pubNC, err := dialWith(t, url, cred.Creds)
	if err != nil {
		t.Fatalf("reconnecting for the permission check: %v", err)
	}
	defer pubNC.Close()
	pubNC.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
		select {
		case violations <- e:
		default:
		}
	})

	// Allowed: a job log line, which FleetRunnerGrant permits.
	allowed := topology.LogSubject("job-1")
	if err := pubNC.Publish(allowed, []byte("x")); err != nil {
		t.Fatalf("publishing to %q: %v", allowed, err)
	}
	if err := pubNC.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case e := <-violations:
		t.Fatalf("publishing to the permitted subject %q was refused: %v", allowed, e)
	case <-time.After(500 * time.Millisecond):
	}

	// Denied: a subject no Runner has any business publishing to.
	denied := "pleiades.jobs.requested"
	if err := pubNC.Publish(denied, []byte("x")); err != nil {
		t.Fatalf("publishing to %q: %v", denied, err)
	}
	if err := pubNC.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case e := <-violations:
		if !strings.Contains(strings.ToLower(e.Error()), "permission") {
			t.Errorf("publishing to %q failed with %v, which is not a permissions violation", denied, e)
		}
	case <-time.After(3 * time.Second):
		t.Errorf("publishing to %q was permitted; a Runner can forge a job launch", denied)
	}
}
