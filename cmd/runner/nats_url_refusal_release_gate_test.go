// Package main_test drives the built runner binary against mesh
// configurations it has to refuse at startup.
//
// It is the Runner half of cmd/controller's file of the same name, which
// carries the reasoning both share. What differs here is how "at startup"
// can be observed: a Runner has no listener and no database to assert were
// untouched, so the claim is measured against topology.ConnectWaitTimeout
// instead.
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
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// This file is the "refused at startup" half of Phase 96d's Adversarial
// Pattern Justification, for the Runner. cmd/controller's file of the same
// name is its sibling, and its doc comment carries the reasoning both
// share.
//
// What differs here is how "at startup" can be observed at all. The
// Controller has a listener and a database, so its gate asserts that
// neither was touched. A Runner has neither, so the claim has to be
// measured instead: see refusalBudget below.

// refusalBudget is how long a refusal at startup may take.
//
// It is derived rather than picked. topology.ConnectWaitTimeout bounds how
// long a dial against an unreachable broker blocks, so a Runner that
// reached the dial before refusing cannot possibly finish inside a small
// fraction of it, while one that refused during configuration finishes in
// milliseconds. A fifth of that bound leaves a five-fold margin in each
// direction, which is what keeps this an ordering assertion rather than a
// timing test. Every URL below names an unroutable host precisely so the
// two outcomes are that far apart.
var refusalBudget = topology.ConnectWaitTimeout / 5

// TestNatsURLReleaseGate_TheRunnerRefusesAMisconfiguredBrokerAtStartup
// drives the real Runner binary with each mesh configuration the
// allowlist and the TLS resolver are supposed to reject.
func TestNatsURLReleaseGate_TheRunnerRefusesAMisconfiguredBrokerAtStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Runner startup gate in short mode")
	}
	bin := buildRunner(t)
	realCA := writeThrowawayAnchor(t)

	for _, tc := range []struct {
		name string
		url  string
		ca   string
		want string
	}{
		{
			// The mistake the allowlist exists for: nats.go's own parser
			// does not error on an unimplemented scheme, it falls back to
			// plaintext, so this used to DOWNGRADE silently.
			name: "a scheme nats.go does not implement",
			url:  "tsl://broker.invalid:4222",
			want: "which nats.go does not implement",
		},
		{
			name: "a bare host and port",
			url:  "broker.invalid:4222",
			want: "looks like a bare host and port",
		},
		{
			name: "a list mixing encrypted and plaintext members",
			url:  "tls://a.invalid:4222,nats://b.invalid:4222",
			want: "mixes encrypted and plaintext entries",
		},
		{
			// The dangerous reading of this pair is that the connection
			// is protected, which is why it is a startup error rather
			// than a warning.
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
			// Far longer than a refusal needs, and not the budget being
			// asserted: this is the tripwire that separates "refused"
			// from "went to the broker and is still trying".
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, bin)
			cmd.Dir = t.TempDir()
			env := append(os.Environ(), "NATS_URL="+tc.url, "OTEL_TRACES_EXPORTER=none")
			if tc.ca != "" {
				env = append(env, "NATS_CA_FILE="+tc.ca)
			}
			cmd.Env = env

			started := time.Now()
			out, err := cmd.CombinedOutput()
			elapsed := time.Since(started)

			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("the runner did not refuse NATS_URL=%q; it went on toward the broker and was still running when the deadline fired:\n%s", tc.url, out)
			}
			var exitErr *exec.ExitError
			switch {
			case err == nil:
				t.Fatalf("the runner STARTED with NATS_URL=%q. A scheme nats.go does not implement silently selects plaintext, so starting is the failure here, not the success:\n%s", tc.url, out)
			case !errors.As(err, &exitErr):
				t.Fatalf("running the runner: %v\n%s", err, out)
			case exitErr.ExitCode() != 1:
				t.Fatalf("the runner exited %d, want 1: a configuration refusal is a deliberate exit, and any other code means it died for some other reason:\n%s", exitErr.ExitCode(), out)
			}

			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("the refusal does not contain %q:\n%s", tc.want, out)
			}

			// THE ASSERTION THAT MAKES THIS "AT STARTUP". See
			// refusalBudget for why this measurement is an ordering claim
			// rather than a timing one.
			if elapsed > refusalBudget {
				t.Errorf("the runner took %s to refuse %q, over the %s budget. A refusal made while reading configuration is immediate; anything approaching topology.ConnectWaitTimeout (%s) means it dialed the broker first", elapsed, tc.url, refusalBudget, topology.ConnectWaitTimeout)
			}
		})
	}
}

// TestNatsURLReleaseGate_TheRunnerAcceptsEveryImplementedScheme is the
// positive control, without which a Runner that refused EVERY url would
// pass every case above while being completely broken.
//
// It asserts that a later stage was reached rather than merely that no
// refusal was printed, because an absence is also what a process that
// died earlier for an unrelated reason produces. Here the later stage is
// observed as TIME: every url names an unroutable host, so an accepted
// scheme must carry the Runner into the dial and keep it there for
// topology.ConnectWaitTimeout. Taking that long is the evidence.
func TestNatsURLReleaseGate_TheRunnerAcceptsEveryImplementedScheme(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Runner startup gate in short mode")
	}
	bin := buildRunner(t)

	for _, url := range []string{
		"nats://broker.invalid:4222",
		"tls://broker.invalid:4222",
		"ws://broker.invalid:8080",
		"wss://broker.invalid:443",
	} {
		t.Run(url, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, bin)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "NATS_URL="+url, "OTEL_TRACES_EXPORTER=none")

			started := time.Now()
			out, _ := cmd.CombinedOutput()
			elapsed := time.Since(started)

			for _, refusal := range []string{
				"invalid NATS_URL",
				"which nats.go does not implement",
				"looks like a bare host and port",
			} {
				if strings.Contains(string(out), refusal) {
					t.Fatalf("the runner refused the implemented scheme %q with %q:\n%s", url, refusal, out)
				}
			}
			if elapsed <= refusalBudget {
				t.Fatalf("the runner gave up on %q after only %s, which is inside the refusal budget. An accepted scheme has to reach the dial and wait out topology.ConnectWaitTimeout (%s), so finishing this fast means it never got there and this case proves nothing:\n%s", url, elapsed, topology.ConnectWaitTimeout, out)
			}
		})
	}
}

// writeThrowawayAnchor returns the path of a real, parseable certificate,
// so the plaintext-URL case fails for the reason under test rather than
// because the file could not be read.
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
