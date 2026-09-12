// Phase 101c's first Release Gate: a real nats-server in operator mode,
// with JetStream ON, running THIS PLATFORM'S OWN provisioning and dispatch
// code under a minted credential.
//
// It exists because Phase 101b's gate could not have caught what 101b
// shipped. That gate runs a broker with JetStream off and asserts core
// publishes, and every defect below lives on the JetStream control plane:
// an account whose claims disabled JetStream outright, a system account
// that has to exist before JetStream will start at all, two consumer
// permissions that ended one wildcard past the subject the driver sends,
// a stream permission with `>` in a non-final token, and a missing dead
// letter subject. Five defects, one blind spot.
//
// So this gate's rule is that it calls the real functions. Not a hand
// built stream config, not a hand built consumer, not nats.Connect:
// topology.ProvisionStream, topology.BindLockBucket,
// topology.DispatchConsumerConfig and topology.Connect, which is what
// cmd/controller and cmd/runner call. A gate that assembled its own
// equivalents would have passed against every one of those five defects.

package meshid_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// jetStreamOperatorModeConfig is operator mode as this platform actually
// needs it, which is a strictly larger thing than what Phase 101b proved.
//
// Two lines here are not decoration, and both were measured against a real
// nats-server 2.14.4 rather than reasoned about:
//
//   - system_account, and a second minted account to point it at. Without
//     it the server does not merely lack a feature, it REFUSES TO START:
//     "Can't start JetStream: setting up internal jetstream subscriptions
//     failed: system account not setup", followed by exit code 1. An
//     operator-mode deployment of this platform therefore needs two
//     accounts minted, and 101b minted one.
//   - That system account must have JetStream DISABLED, which is why
//     meshid.NewSystemAccount exists as its own constructor. Enabling it
//     is refused just as fatally: "Not allowed to enable JetStream on the
//     system account".
func jetStreamOperatorModeConfig(op *meshid.Operator, sys, app *meshid.Account) string {
	return fmt.Sprintf(`
operator: %s
system_account: %s
resolver: MEMORY
resolver_preload: {
  %s: %s
  %s: %s
}
jetstream: {
  store_dir: /data
}
`, op.JWT, sys.Subject, sys.Subject, sys.JWT, app.Subject, app.JWT)
}

func TestReleaseGate_TheRealControlPlaneRunsUnderAMintedIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	op, err := meshid.NewOperator("pleiades-gate")
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

	// ---- Act 1: the control. The broker really is enforcing. ----
	//
	// Without this the rest proves only that a broker accepts connections.
	if conn, err := topology.Connect(ctx, url, logger, "anonymous"); err == nil {
		conn.Close()
		t.Fatal("an anonymous client connected to the gate's broker, so every later act proves nothing about enforcement")
	}

	// ---- Act 2: the Controller provisions the real topology. ----
	//
	// This is the act the ControllerGrant defect failed. `>` in a
	// non-final token is not a wildcard, so the stream permission matched
	// nothing, the request got no reply, and this surfaced as a context
	// deadline rather than as a permissions error. A gate with a generous
	// timeout and no assertion on the error would have looked slow rather
	// than broken.
	ctrlCred, err := issuer.Issue(meshid.ControllerGrant("gate-controller"), time.Hour)
	if err != nil {
		t.Fatalf("issuing the Controller credential: %v", err)
	}
	ctrlConn, err := topology.Connect(ctx, url, logger, "gate-controller", topology.WithCredentials(ctrlCred.Creds))
	if err != nil {
		t.Fatalf("the Controller could not connect with its own minted credential: %v", err)
	}
	defer ctrlConn.Close()
	ctrlJS, err := jetstream.New(ctrlConn)
	if err != nil {
		t.Fatalf("jetstream.New for the Controller: %v", err)
	}

	provCtx, cancelProv := context.WithTimeout(ctx, 20*time.Second)
	defer cancelProv()
	if _, _, err := topology.ProvisionStream(provCtx, ctrlJS, topology.DefaultOutageBudget, false); err != nil {
		t.Fatalf("the Controller could not provision the PLEIADES stream under ControllerGrant: %v", err)
	}
	if _, err := topology.BindLockBucket(provCtx, ctrlJS, topology.StreamProvisioner); err != nil {
		t.Fatalf("the Controller could not provision the lock bucket under ControllerGrant: %v", err)
	}

	// ---- Act 3: the Runner's own three operations. ----
	//
	// Create, pull and probe are exactly the three the consumer grant
	// broke, and all three failed silently: a denied JetStream request
	// gets no reply at all, so a Runner under the shipped grant
	// authenticated, created nothing, pulled nothing and reported
	// unhealthy without a single error naming permissions.
	runnerCred, err := issuer.Issue(meshid.FleetRunnerGrant("gate-runner"), time.Hour)
	if err != nil {
		t.Fatalf("issuing the Runner credential: %v", err)
	}
	runConn, err := topology.Connect(ctx, url, logger, "gate-runner", topology.WithCredentials(runnerCred.Creds))
	if err != nil {
		t.Fatalf("the Runner could not connect with its own minted credential: %v", err)
	}
	defer runConn.Close()
	runJS, err := jetstream.New(runConn)
	if err != nil {
		t.Fatalf("jetstream.New for the Runner: %v", err)
	}

	runCtx, cancelRun := context.WithTimeout(ctx, 20*time.Second)
	defer cancelRun()
	consumer, err := runJS.CreateOrUpdateConsumer(runCtx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		t.Fatalf("the Runner could not create its dispatch consumer under FleetRunnerGrant: %v", err)
	}
	if _, err := consumer.Info(runCtx); err != nil {
		t.Fatalf("the Runner's heartbeat probe (Consumer.Info) was denied under FleetRunnerGrant: %v", err)
	}

	// ---- Act 4: a real dispatch crosses the authenticated mesh. ----
	//
	// The positive control the negative acts need. Act 5 asserts a Runner
	// cannot do certain things; without a dispatch that genuinely arrives,
	// "it could not" and "there was nothing to get" are the same result.
	const deviceID = "router1.example.com"
	if _, err := ctrlJS.Publish(runCtx, topology.DispatchSubject(deviceID), []byte(`{"job_id":"gate-1"}`)); err != nil {
		t.Fatalf("the Controller could not publish a dispatch under ControllerGrant: %v", err)
	}

	var got jetstream.Msg
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && got == nil {
		batch, err := consumer.FetchNoWait(1)
		if err != nil {
			t.Fatalf("the Runner's pull (FetchNoWait) was denied under FleetRunnerGrant: %v", err)
		}
		for m := range batch.Messages() {
			got = m
		}
		if err := batch.Error(); err != nil {
			t.Fatalf("draining the Runner's fetch: %v", err)
		}
		if got == nil {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if got == nil {
		t.Fatal("the Runner never received the dispatch the Controller published, on an authenticated mesh where both operations reported success")
	}
	if got.Subject() != topology.DispatchSubject(deviceID) {
		t.Fatalf("the Runner received %q, want the dispatch subject %q", got.Subject(), topology.DispatchSubject(deviceID))
	}
	if err := got.Ack(); err != nil {
		t.Fatalf("the Runner could not ack under FleetRunnerGrant: %v", err)
	}

	// ---- Act 4b: the run journal crosses the mesh in both directions. ----
	//
	// Phase 40 added a subject, and a grant entry for it, and a test
	// asserting that grant permits it. FAILURE_PATTERNS.md #207 is
	// precisely about why that last assertion proves nothing on its own:
	// the grant and the assertion were written by the same person at the
	// same moment from the same understanding. Only a real broker can
	// contradict them, and a denied publish here would show up not as a
	// permissions error but as a JetStream request that never gets a
	// reply.
	if _, err := runJS.Publish(runCtx, topology.JournalSubject("gate-1"), []byte(`{"job_id":"gate-1"}`)); err != nil {
		t.Fatalf("the Runner could not publish a run journal batch under FleetRunnerGrant: %v", err)
	}
	// And the Controller's own dead letter path for that consumer. A
	// handler error routes into event.HandleDeliveryFailure, which
	// returns BEFORE msg.Term() if this publish is denied, so a batch the
	// store keeps refusing would be neither dead-lettered nor terminated.
	dlq := topology.DeadLetterSubject(topology.JournalSubject("gate-1"))
	if _, err := ctrlJS.Publish(runCtx, dlq, []byte(`{"dead":true}`)); err != nil {
		t.Fatalf("the Controller could not dead-letter a run journal batch under ControllerGrant: %v", err)
	}

	// ---- Act 5: the negative controls, which are the deliverable. ----
	//
	// A Runner must not be able to forge a job launch, and must not be
	// able to RESHAPE the one shared durable. The second is the sharper
	// of the two: CreateOrUpdateConsumer is an upsert, so a Runner
	// permitted to create with any filter could widen the fleet consumer
	// to `pleiades.>` and read every other device's dispatch payload,
	// which carries resolved plaintext credentials. Pinning the filter
	// into the granted API subject is what forbids it.
	// A core publish is fire and forget, so a permission violation never
	// comes back from Publish or from Flush: the server reports it
	// asynchronously and the error handler is the only place it appears.
	// Asserting on Flush's error would silently pass on every subject.
	violations := make(chan error, 4)
	runConn.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
		select {
		case violations <- e:
		default:
		}
	})
	if err := runConn.Publish(topology.JobRequestedSubject(), []byte(`{"forged":true}`)); err != nil {
		t.Fatalf("publishing the forged launch: %v", err)
	}
	if err := runConn.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case e := <-violations:
		if !strings.Contains(strings.ToLower(e.Error()), "permission") {
			t.Errorf("the forged job launch failed with %v, which is a refusal but not a permissions violation", e)
		}
	case <-time.After(3 * time.Second):
		t.Error("a Runner published to the job launch subject; a compromised Runner can forge a job launch")
	}

	widened := topology.DispatchConsumerConfig()
	widened.FilterSubject = "pleiades.>"
	wideCtx, cancelWide := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWide()
	if _, err := runJS.CreateOrUpdateConsumer(wideCtx, topology.StreamName, widened); err == nil {
		t.Error("a Runner reshaped the shared dispatch consumer to a wider filter, which lets it read every other device's dispatch payload")
	} else if !errors.Is(err, context.DeadlineExceeded) && !isPermissionish(err) {
		t.Logf("the widening attempt failed with %v, which is a refusal but not the expected shape", err)
	}
}

// isPermissionish reports whether an error looks like a NATS authorization
// refusal. A denied JetStream API request gets NO REPLY rather than an
// error reply, so the honest signal is usually a timeout; this exists so
// the gate can say which it saw rather than asserting a string.
func isPermissionish(err error) bool {
	return errors.Is(err, nats.ErrAuthorization) ||
		errors.Is(err, nats.ErrPermissionViolation) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, nats.ErrTimeout)
}
