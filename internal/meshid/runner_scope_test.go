// Package meshid_test: the runner scope's issuance rules (0.5.0). The broker
// enforces the name a Runner credential carries, so a name that is not one
// subject token is refused before anything is minted, and the system account
// gets no runner scope at all.
package meshid_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
)

func TestRunnerIssuerRefusesANameThatIsNotOneSubjectToken(t *testing.T) {
	op, err := meshid.NewOperator("runner-scope")
	if err != nil {
		t.Fatal(err)
	}
	app, err := meshid.NewAccount(op, "PLEIADES")
	if err != nil {
		t.Fatal(err)
	}
	runners, err := meshid.NewRunnerIssuer(app.Subject, app.RunnerSeed)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "router.example.com", "a b", "a*", "a>", "a/b"} {
		if _, err := runners.Issue(bad, time.Hour); err == nil {
			t.Errorf("Issue(%q) minted a credential under a name that is not one subject token", bad)
		}
	}
	if _, err := runners.Issue("runner-a", time.Hour); err != nil {
		t.Errorf("Issue(runner-a) = %v, want a credential", err)
	}
}

func TestRunnerIssuerRefusesAMalformedSeed(t *testing.T) {
	if _, err := meshid.NewRunnerIssuer("ACCOUNT", []byte("not a seed")); err == nil {
		t.Fatal("NewRunnerIssuer accepted a seed that is not an nkey seed")
	}
}

func TestOnlyAnApplicationAccountGetsARunnerScope(t *testing.T) {
	op, err := meshid.NewOperator("runner-scope")
	if err != nil {
		t.Fatal(err)
	}
	sys, err := meshid.NewSystemAccount(op, "SYS")
	if err != nil {
		t.Fatal(err)
	}
	if len(sys.RunnerSeed) != 0 {
		t.Error("the system account was given a runner scope; it has no Runners")
	}
	app, err := meshid.NewAccount(op, "PLEIADES")
	if err != nil {
		t.Fatal(err)
	}
	if len(app.RunnerSeed) == 0 {
		t.Error("the application account has no runner scope")
	}
}
