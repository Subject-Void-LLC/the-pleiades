// Package topology_test: the Runner-named subjects (0.5.0). The broker matches a
// credential's name against the subject token, so the rules for what a token is
// and how a subject reads its Runner back are held here case by case.
package topology_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

func TestIsSubjectTokenRefusesEverythingThatIsNotOneToken(t *testing.T) {
	for _, bad := range []string{"", "a.b", "a*", "a>", "a/b", `a\b`, "a b", "a\tb", "a\x01", "a\x7f"} {
		if topology.IsSubjectToken(bad) {
			t.Errorf("IsSubjectToken(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{"runner-a", "runner_1", "R2"} {
		if !topology.IsSubjectToken(good) {
			t.Errorf("IsSubjectToken(%q) = false, want true", good)
		}
	}
}

func TestRunnerTokenKeepsACredentialNameAndSanitizesAHostName(t *testing.T) {
	if got := topology.RunnerToken("runner-a"); got != "runner-a" {
		t.Errorf("RunnerToken(credential name) = %q, want it unchanged", got)
	}
	got := topology.RunnerToken("router.example.com")
	if !topology.IsSubjectToken(got) || strings.Contains(got, ".") {
		t.Errorf("RunnerToken(host name) = %q, want one subject token", got)
	}
}

func TestFactsAndJournalRunnerReadTheRunnerBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		subject string
		fn      func(string) (string, bool)
		want    string
		ok      bool
	}{
		{"facts names its runner", topology.FactsSubject("runner-a", "job-1"), topology.FactsRunner, "runner-a", true},
		{"journal names its runner", topology.JournalSubject("runner-b", "job-1"), topology.JournalRunner, "runner-b", true},
		{"facts subject read as journal", topology.FactsSubject("runner-a", "job-1"), topology.JournalRunner, "", false},
		{"journal subject read as facts", topology.JournalSubject("runner-a", "job-1"), topology.FactsRunner, "", false},
		{"no job token", "pleiades.jobs.facts.runner-a", topology.FactsRunner, "", false},
		{"empty runner", "pleiades.jobs.facts..job-1", topology.FactsRunner, "", false},
		{"job token holds a dot", "pleiades.jobs.facts.runner-a.job.1", topology.FactsRunner, "", false},
		{"not a jobs subject", "pleiades.events.device.created", topology.FactsRunner, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.fn(tc.subject)
			if got != tc.want || ok != tc.ok {
				t.Errorf("(%q) = (%q, %v), want (%q, %v)", tc.subject, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestRunnerNameFromCredentialsReadsTheNameTheBrokerEnforces(t *testing.T) {
	op, err := meshid.NewOperator("runner-name")
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
	cred, err := runners.Issue("runner-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, err := topology.RunnerNameFromCredentials(cred.Creds)
	if err != nil || got != "runner-a" {
		t.Fatalf("RunnerNameFromCredentials = %q, %v; want runner-a", got, err)
	}
	if _, err := topology.RunnerNameFromCredentials([]byte("not a credential")); err == nil {
		t.Fatal("RunnerNameFromCredentials accepted bytes that are not a credential")
	}
}
