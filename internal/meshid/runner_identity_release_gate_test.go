// Release gate for broker-enforced Runner identity (0.5.0): a Runner
// credential minted under the account's runner scope publishes facts and
// journal batches only under its own name.
//
// It runs a real nats-server in operator mode, as the other meshid gates do,
// and mints credentials through RunnerIssuer, the path the Controller uses.
// Each Runner must publish under its own name and be refused under another's.
package meshid_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/nats-io/nats.go"
)

func TestReleaseGate_ARunnerPublishesOnlyUnderItsOwnName(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	op, err := meshid.NewOperator("pleiades-identity-gate")
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
	if len(app.RunnerSeed) == 0 {
		t.Fatal("the application account carries no runner scope, so its Runners cannot be named by the broker")
	}
	url := startOperatorModeBroker(t, jetStreamOperatorModeConfig(op, sys, app))
	runners, err := meshid.NewRunnerIssuer(app.Subject, app.RunnerSeed)
	if err != nil {
		t.Fatalf("building the runner issuer: %v", err)
	}

	credA, err := runners.Issue("runner-a", time.Hour)
	if err != nil {
		t.Fatalf("issuing runner-a: %v", err)
	}
	credB, err := runners.Issue("runner-b", time.Hour)
	if err != nil {
		t.Fatalf("issuing runner-b: %v", err)
	}

	// publish sends one message as the credential and reports whether the
	// broker refused it. A denied publish surfaces on the error handler, not
	// as a Publish error, so the test flushes and then waits briefly.
	publish := func(t *testing.T, creds []byte, name, subject string) (refused bool) {
		t.Helper()
		var mu sync.Mutex
		var denied string
		conn, err := nats.Connect(url,
			nats.Name(name),
			nats.UserCredentialBytes(creds),
			nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
				if strings.Contains(err.Error(), "Permissions Violation") {
					mu.Lock()
					denied = err.Error()
					mu.Unlock()
				}
			}))
		if err != nil {
			t.Fatalf("%s could not connect: %v", name, err)
		}
		defer conn.Close()
		if err := conn.Publish(subject, []byte("x")); err != nil {
			t.Fatalf("%s could not queue a publish on %s: %v", name, subject, err)
		}
		if err := conn.Flush(); err != nil {
			t.Fatalf("%s flush on %s: %v", name, subject, err)
		}
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return denied != ""
	}

	if publish(t, credA.Creds, "runner-a", "facts.runner-a.job-1") {
		t.Errorf("runner-a was refused publishing under its own name")
	}
	if publish(t, credA.Creds, "runner-a", "journal.runner-a.job-1") {
		t.Errorf("runner-a was refused publishing its own journal")
	}
	if !publish(t, credA.Creds, "runner-a", "facts.runner-b.job-1") {
		t.Errorf("runner-a was NOT refused publishing under runner-b's name: the broker does not enforce identity")
	}
	if !publish(t, credB.Creds, "runner-b", "journal.runner-a.job-1") {
		t.Errorf("runner-b was NOT refused publishing under runner-a's name")
	}
}
