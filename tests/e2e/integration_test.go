//go:build integration

// This file is Phase 18's Release Gate: the Grand Integration Test.
//
// What it proves, in one sentence: a real HTTP caller, authenticated by a
// real signed token against the real router, causes a real PostgreSQL
// row, a real NATS message per targeted device carrying that device's
// real attributes, real execution by a separate real Runner process, and
// a real terminal record readable back out of the database.
//
// Layers it must not mock, and does not: the HTTP server (the real
// cmd/controller binary, a real socket, the real chi router with its real
// auth, rate limiting and per-route scope enforcement); the token (really
// signed, really verified); the database (real PostgreSQL, real versioned
// migrations, real envelope encryption); the message bus (real NATS
// JetStream with the real stream and consumer topology); and the Runner
// (the real cmd/runner binary joining the real durable consumer group).
//
// Layers it deliberately does not exercise, and why. There is no real SSH
// target, because the runbook fixture names the builtin "noop" action
// which needs no transport, and reaching a real device over real SSH is
// a claim cmd/runner's own SSH release gate already carries. There is no
// JWKS or RS256, already proven by cmd/controller's JWKS release gate.
// There is no multi-replica leader election, already proven by
// cmd/controller's leader election release gate. Duplicating any of those
// here would cost minutes and prove nothing new.
package e2e

import (
	"net/http"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
)

// TestGrandIntegration drives one runbook launch through the entire mesh
// and checks every observable consequence of it.
//
// Read the assertion helpers in integration_assert_test.go alongside
// this: each one documents what would have to break for it to fail, which
// is the standard this phase's Adversarial gate sets. An assertion that
// cannot fail is not a test.
func TestGrandIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the grand integration test in short mode")
	}

	h := startHarness(t)

	// The issuer signs with the exact secret the controller subprocess
	// was given, which is the whole reason authtest grew a
	// secret-taking constructor: a random binary secret cannot survive an
	// environment variable.
	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)

	adminToken := issuer.Token(t, &auth.Identity{Subject: "e2e-admin", Role: auth.RoleAdmin})

	// A separate issuer with a different secret, used only to prove that
	// signature verification actually runs. Without this case, the
	// missing-header 401 below would still pass even if verification were
	// a no-op.
	forgedIssuer := authtest.NewWithSecret(t, "a-completely-different-signing-secret-32b", harnessJWTIssuer, harnessJWTAudience)
	forgedToken := forgedIssuer.Token(t, &auth.Identity{Subject: "e2e-admin", Role: auth.RoleAdmin})

	// Zero trust, exercised before anything succeeds, so a job row
	// appearing later cannot be attributed to one of these.
	t.Run("rejects an unauthenticated launch", func(t *testing.T) {
		status, body := h.dispatch(t, "", targetGroup, harnessRunbookID)
		if status != http.StatusUnauthorized {
			t.Fatalf("an unauthenticated dispatch returned %d, want 401. Body: %s", status, body)
		}
	})

	t.Run("rejects a forged token", func(t *testing.T) {
		status, body := h.dispatch(t, forgedToken, targetGroup, harnessRunbookID)
		if status != http.StatusUnauthorized {
			t.Fatalf("a dispatch signed with the wrong secret returned %d, want 401. Body: %s", status, body)
		}
	})

	// Neither rejected request may have reached the handler. Checking the
	// database is what proves that: a 401 on its own only proves a number
	// was written to the response.
	h.assertNoJobsPersisted(t)

	// Observers are created before the launch, so nothing depends on
	// winning a race with the publisher.
	dispatchObserver := h.observeDispatches(t)

	// The launch itself.
	status, body := h.dispatch(t, adminToken, targetGroup, harnessRunbookID)
	if status != http.StatusAccepted {
		t.Fatalf("dispatch returned %d, want 202. Body: %s\n%s", status, body, h.controller.output())
	}
	if got := requireStringField(t, body, "status"); got != "accepted" {
		t.Fatalf("dispatch response status = %q, want %q", got, "accepted")
	}
	jobID := requireStringField(t, body, "job_id")

	h.assertJobIDIsServerMinted(t, jobID)

	// The API's own view, polled the way a real client is told to poll it.
	job := h.pollJobUntilTerminal(t, adminToken, jobID)
	h.assertJobView(t, job, jobID)

	// The same facts, read straight out of PostgreSQL, which is the thing
	// the Release Gate's own wording asks for and the previous version of
	// this test never did.
	h.assertJobInDatabase(t, jobID)

	// The per-device messages, decoded and checked field by field against
	// what was seeded.
	h.assertDispatchPayloads(t, dispatchObserver, jobID)

	// Proof the Runner process actually executed, rather than the
	// controller merely having published.
	h.assertJobLogEvents(t, jobID)

	// Negative controls.
	h.assertNoDeadLetters(t)
	h.assertPropertiesEncryptedAtRest(t)
}

// TestGrandIntegration_UnknownGroupFailsClosed proves a launch naming a
// group that does not exist completes with nothing dispatched.
//
// What would have to break for this to fail: GetGroup's membership
// predicate ceasing to fail closed, so that an unmatched selector
// returned the whole fleet instead of nothing. That is the single most
// dangerous failure this inventory layer has, since it would silently
// turn a targeted change into a fleet-wide one.
func TestGrandIntegration_UnknownGroupFailsClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the grand integration test in short mode")
	}

	h := startHarness(t)
	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	adminToken := issuer.Token(t, &auth.Identity{Subject: "e2e-admin", Role: auth.RoleAdmin})

	status, body := h.dispatch(t, adminToken, "no-such-group", harnessRunbookID)
	if status != http.StatusAccepted {
		t.Fatalf("dispatch against an unknown group returned %d, want 202. Body: %s", status, body)
	}
	jobID := requireStringField(t, body, "job_id")

	job := h.pollJobUntilTerminal(t, adminToken, jobID)
	if job.State != "completed" {
		t.Fatalf("job state = %q, want completed", job.State)
	}
	if job.Dispatched != 0 || job.Skipped != 0 || job.Failed != 0 {
		t.Fatalf("an unknown group produced dispatched=%d skipped=%d failed=%d, want all zero; a selector that failed open would have dispatched to the whole fleet",
			job.Dispatched, job.Skipped, job.Failed)
	}
	if len(job.Tasks) != 0 {
		t.Fatalf("an unknown group produced %d tasks, want 0: %s", len(job.Tasks), describeTasks(job))
	}
}
