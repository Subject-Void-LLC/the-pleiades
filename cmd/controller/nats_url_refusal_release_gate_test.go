// Package main_test drives the built controller binary against mesh
// configurations it has to refuse, and asserts WHEN it refuses them.
//
// The ordering is the subject, not the exit code, which is why this lives
// beside the binary rather than in tests/e2e: that package reaches the
// controller as a subprocess, so Go's test cache sees no dependency edge to
// this source and would replay a stale pass after an edit to main.go.
package main_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// This file is the "refused at startup" half of Phase 96d's Adversarial
// Pattern Justification, for the Controller.
//
// "At startup" is the whole claim and it is not rhetorical. Until Phase
// 96d closed, both mesh configuration checks lived at the dial, which is
// on the far side of net.Listen and ent.OpenDatabaseReporting. A wrong
// scheme therefore cost a bound port, a 200 on /healthz and a full schema
// migration before the process exited 1, which means a Kubernetes
// liveness probe went green on a Controller that was already doomed, and
// every restart of a deployment whose NATS_URL can never work paid for
// the migration again.
//
// So the two assertions that carry the claim are NOT the exit code. They
// are that the database directory does not exist and that the listening
// line was never printed. The exit code and the message only prove that
// it refused; those two prove WHEN.
//
// It lives in cmd/controller rather than tests/e2e deliberately. Its
// entire subject is the statement ordering inside this package's main.go,
// and tests/e2e reaches the binary as a subprocess, so Go's test cache
// sees no dependency edge to this source and would replay a stale PASS
// from before an edit. Here, editing main.go invalidates it.

// TestNatsURLReleaseGate_TheControllerRefusesAMisconfiguredBrokerBeforeItBindsOrMigrates
// drives the real binary with each mesh configuration the allowlist and
// the TLS resolver are supposed to reject.
func TestNatsURLReleaseGate_TheControllerRefusesAMisconfiguredBrokerBeforeItBindsOrMigrates(t *testing.T) {
	realCA := writeThrowawayAnchor(t)

	for _, tc := range []struct {
		name string
		url  string
		ca   string
		want string
	}{
		{
			// The mistake the allowlist exists for. nats.go's own parser
			// does not error on an unimplemented scheme, it falls back to
			// plaintext, so this used to DOWNGRADE silently rather than
			// fail.
			name: "a scheme nats.go does not implement",
			url:  "tsl://broker.invalid:4222",
			want: "which nats.go does not implement",
		},
		{
			// url.Parse reads everything before the colon as a scheme, so
			// this does NOT reach the empty-scheme branch, and the generic
			// message would have told an operator that "broker" is an
			// unimplemented transport. That is why ValidateNatsURL spends
			// a branch on saying so, and why this case is here.
			name: "a bare host and port",
			url:  "broker.invalid:4222",
			want: "looks like a bare host and port",
		},
		{
			// Which member of a list a client picks is not the caller's to
			// control, so the plaintext one decides what an observer sees.
			name: "a list mixing encrypted and plaintext members",
			url:  "tls://a.invalid:4222,nats://b.invalid:4222",
			want: "mixes encrypted and plaintext entries",
		},
		{
			// The second refusal kind, and the more dangerous reading of
			// the two: somebody believes this connection is protected and
			// it is not.
			name: "a certificate authority named for a plaintext url",
			url:  "nats://broker.invalid:4222",
			ca:   realCA,
			want: "which does not encrypt",
		},
		{
			name: "a certificate authority that is not a certificate",
			url:  "tls://broker.invalid:4222",
			ca:   writeNotACertificate(t),
			want: "contains no PEM certificate",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A path one level under a directory that does not exist, so
			// that directory's existence afterwards is a fact about
			// whether the Controller opened its database rather than
			// about this test's own setup.
			dbDir := filepath.Join(t.TempDir(), "never-created")
			dbPath := filepath.Join(dbDir, "controller.db")

			// Sixty seconds is far longer than a refusal needs and is not
			// the budget being asserted. It is the tripwire: every URL
			// above names an unroutable host, so a build that did NOT
			// refuse would go on to dial it, and topology's reconnect
			// machinery would keep the process alive well past any short
			// deadline. Hitting this therefore means exactly one thing,
			// and the first branch below says which.
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, binPath)
			// Away from the source tree: a self-provisioning Controller
			// resolves its certificate directory relative to its working
			// directory, and this test must not write one into the repo.
			cmd.Dir = t.TempDir()
			env := append(setupEnv(dbPath),
				"JWT_SECRET="+strings.Repeat("j", 40),
				"NATS_URL="+tc.url)
			if tc.ca != "" {
				env = append(env, "NATS_CA_FILE="+tc.ca)
			}
			cmd.Env = env

			out, err := cmd.CombinedOutput()

			// A three-way branch, because "did not refuse" has two very
			// different shapes and they deserve different words.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("the controller did not refuse NATS_URL=%q; it went on toward the broker and was still running when the deadline fired, which is the pre-96d behavior this gate exists to forbid:\n%s", tc.url, out)
			}
			var exitErr *exec.ExitError
			switch {
			case err == nil:
				t.Fatalf("the controller STARTED with NATS_URL=%q. A scheme nats.go does not implement silently selects plaintext, so starting is the failure, not the success:\n%s", tc.url, out)
			case !errors.As(err, &exitErr):
				t.Fatalf("running the controller: %v\n%s", err, out)
			case exitErr.ExitCode() != 1:
				t.Fatalf("the controller exited %d, want 1: a configuration refusal is a deliberate exit, and any other code means it died for some other reason:\n%s", exitErr.ExitCode(), out)
			}

			// The refusal names the real problem rather than a generic
			// one, which is the difference between a message an operator
			// can act on and one they have to guess at.
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("the refusal does not contain %q:\n%s", tc.want, out)
			}

			// THE TWO ASSERTIONS THAT MAKE THIS "AT STARTUP" RATHER THAN
			// "EVENTUALLY".
			//
			// The database directory does not exist, so the schema was
			// never migrated: the expensive, shared half of starting up.
			if _, statErr := os.Stat(dbDir); !os.IsNotExist(statErr) {
				t.Errorf("the controller created %s, so it opened and migrated its database before refusing a broker URL it could never have used", dbDir)
			}
			// And the listening line was never printed, so nothing ever
			// saw this process answer a probe.
			if strings.Contains(string(out), "controller listening") {
				t.Errorf("the controller bound its listener before refusing, so a liveness probe saw a healthy controller that was already exiting:\n%s", out)
			}
		})
	}
}

// TestNatsURLReleaseGate_TheControllerAcceptsEveryImplementedScheme is the
// positive control, and the refusal gate above is worth very little
// without it: a binary that refused EVERY url would pass all five cases
// there while being completely broken.
//
// It asserts a later stage was REACHED rather than merely that no refusal
// was printed, because an absence is exactly what a process that died for
// some unrelated earlier reason also produces. The marker is the envelope
// encryption failure, which main.go raises well after the mesh check (the
// check is in the configuration block that ends with the masking logger;
// envelope encryption is set up after the listener is already bound).
// MASTER_ENCRYPTION_KEY is deliberately left unset so that failure is the
// deterministic outcome for every case here, which is what makes it a
// marker rather than a coincidence.
func TestNatsURLReleaseGate_TheControllerAcceptsEveryImplementedScheme(t *testing.T) {
	// The line main.go logs when it gets past the mesh configuration and
	// on to a stage that has nothing to do with the broker.
	const reachedALaterStage = "failed to init envelope encryption"

	for _, url := range []string{
		"nats://broker.invalid:4222",
		"tls://broker.invalid:4222",
		"ws://broker.invalid:8080",
		"wss://broker.invalid:443",
	} {
		t.Run(url, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, binPath)
			cmd.Dir = t.TempDir()
			cmd.Env = append(setupEnv(filepath.Join(t.TempDir(), "controller.db")),
				"JWT_SECRET="+strings.Repeat("j", 40),
				"NATS_URL="+url)

			out, _ := cmd.CombinedOutput()
			for _, refusal := range []string{
				"invalid NATS_URL",
				"which nats.go does not implement",
				"looks like a bare host and port",
			} {
				if strings.Contains(string(out), refusal) {
					t.Fatalf("the controller refused the implemented scheme %q with %q:\n%s", url, refusal, out)
				}
			}
			if !strings.Contains(string(out), reachedALaterStage) {
				t.Fatalf("the controller never reached the stage that logs %q with NATS_URL=%q, so this case proves nothing about the scheme being accepted: it may have died earlier for an unrelated reason:\n%s", reachedALaterStage, url, out)
			}
		})
	}
}

// writeThrowawayAnchor returns the path of a real, parseable certificate,
// so the plaintext-URL case above fails for the reason under test rather
// than because the file could not be read.
func writeThrowawayAnchor(t *testing.T) string {
	t.Helper()
	cert, err := tlscert.Generate(t.TempDir(), tlscert.Options{TTL: testsupport.ServingCertTTL})
	if err != nil {
		t.Fatalf("generating a throwaway anchor: %v", err)
	}
	return cert.CertFile
}

// writeNotACertificate returns the path of a regular file holding no PEM,
// which is the shape of an operator pointing NATS_CA_FILE at the wrong
// thing.
func writeNotACertificate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "not-a-certificate.pem")
	if err := os.WriteFile(path, []byte("this is not a certificate\n"), 0o600); err != nil {
		t.Fatalf("writing the decoy: %v", err)
	}
	return path
}
