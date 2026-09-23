// The escalation this phase's Adversarial gate names that the other
// gates do not reach: a credential edited after it was signed.
//
// The other three escalations are already covered and are listed here so
// the set is legible in one place. An EXPIRED credential replayed is
// TestReleaseGate_AnExpiringCredentialEvictsALiveConnection, which
// asserts the connection ends closed and cannot come back. A REVOKED user
// reconnecting is TestReleaseGate_RevokingAUserEvictsItsLiveConnection,
// whose last act dials again with the revoked credential and requires a
// refusal. A SCOPED user reshaping the fleet consumer is act five of
// TestReleaseGate_TheRealControlPlaneRunsUnderAMintedIdentity.
//
// What is left is forgery, and it is the one worth doing against a real
// server rather than reasoning about: the whole trust model rests on the
// broker validating a signature chain and nothing else, so "a modified
// JWT is rejected" is not a property of this code at all. It is a
// property of nats-server, and this platform's security depends on it
// being true.
package meshid_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// TestReleaseGate_ACredentialEditedAfterSigningIsRefused proves the
// signature chain is actually checked.
func TestReleaseGate_ACredentialEditedAfterSigningIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	op, err := meshid.NewOperator("tamper-op")
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
	cred, err := issuer.Issue(meshid.FleetRunnerGrant("honest-runner"), time.Hour)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	// ---- The control, first. The UNMODIFIED credential works. ----
	//
	// Without it, every refusal below is satisfied just as well by a
	// broker that refuses everything, or by a credential this test built
	// wrongly.
	conn, err := topology.Connect(ctx, url, logger, "honest-runner",
		topology.WithCredentials(cred.Creds))
	if err != nil {
		t.Fatalf("the unmodified credential was refused, so no refusal below means anything: %v", err)
	}
	conn.Close()

	// The three edits worth trying, each aimed at a different part of the
	// token. A JWT is three dot-separated base64 segments: header,
	// payload, signature.
	for _, tc := range []struct {
		name string
		edit func(string) string
	}{
		{
			// The payload is where a grant lives, so this is the edit an
			// attacker actually wants: widen your own permissions.
			name: "the claims are altered",
			edit: func(jwt string) string {
				parts := strings.SplitN(jwt, ".", 3)
				if len(parts) != 3 {
					return jwt
				}
				return parts[0] + "." + flipFirst(parts[1]) + "." + parts[2]
			},
		},
		{
			// Altering the signature is the cruder attempt, and the one a
			// corrupted file produces by accident.
			name: "the signature is altered",
			edit: func(jwt string) string {
				parts := strings.SplitN(jwt, ".", 3)
				if len(parts) != 3 {
					return jwt
				}
				return parts[0] + "." + parts[1] + "." + flipFirst(parts[2])
			},
		},
		{
			// Stripping the signature entirely, which is the shape of the
			// classic "alg: none" attack against JWT libraries.
			name: "the signature is removed",
			edit: func(jwt string) string {
				parts := strings.SplitN(jwt, ".", 3)
				if len(parts) != 3 {
					return jwt
				}
				return parts[0] + "." + parts[1] + "."
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tampered := editCredentialJWT(t, string(cred.Creds), tc.edit)

			// Connect, not just parse. A malformed credential that this
			// module refuses locally would prove nothing about the
			// broker, and the broker is what the trust model rests on.
			// topology.Connect refuses some shapes before dialing, which
			// is a pass here too: what must never happen is a connection.
			c, err := topology.Connect(ctx, url, logger, "forged-runner",
				topology.WithCredentials([]byte(tampered)))
			if err == nil {
				c.Close()
				t.Fatal("a credential edited after signing was ACCEPTED; the signature chain is not being checked, and any holder of any credential could grant themselves any permission")
			}
		})
	}
}

// editCredentialJWT applies edit to the JWT inside a .creds body, leaving
// the seed half untouched.
//
// The seed is deliberately left alone. Changing it as well would produce
// a credential that fails for two independent reasons, and a refusal that
// could be caused by either says nothing about which.
func editCredentialJWT(t *testing.T, creds string, edit func(string) string) string {
	t.Helper()
	lines := strings.Split(creds, "\n")
	for i, line := range lines {
		// The token line is the one inside the JWT armor: three
		// dot-separated segments and no armor punctuation.
		if strings.Count(line, ".") == 2 && !strings.HasPrefix(line, "-----") {
			lines[i] = edit(line)
			return strings.Join(lines, "\n")
		}
	}
	t.Fatal("no jwt line found in the credential, so this test edited nothing")
	return creds
}

// flipFirst changes the first character of a base64 segment.
//
// One character, because the claim being tested is that ANY modification
// is refused, and a wholesale replacement would leave open the
// possibility that only large changes are caught.
//
// THE FIRST CHARACTER, NOT THE LAST, and this repository has already paid
// for that distinction once: FAILURE_PATTERNS.md #75 is a JWT forgery
// test that tampered with base64 PADDING BITS instead of the signature,
// so it caught a forgery that had never been forged. The same trap is
// live here. An Ed25519 signature is 64 bytes, which base64url encodes as
// 86 characters carrying 516 bits, so the final character holds four bits
// that decode to nothing. Flipping it often leaves the decoded signature
// byte for byte identical, the broker accepts a credential that was never
// actually altered, and the test fails intermittently while appearing to
// report a security hole.
//
// That was not reasoned about. Flipping the last character made the
// signature case fail roughly one run in three. The first character's six
// bits are always significant, in every segment, whatever its length.
func flipFirst(segment string) string {
	if segment == "" {
		return "A"
	}
	replacement := byte('A')
	if segment[0] == 'A' {
		replacement = 'B'
	}
	return string(replacement) + segment[1:]
}
