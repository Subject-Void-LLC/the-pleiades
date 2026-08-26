package meshid_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/nats-io/nkeys"
)

// FuzzIssue hardens the boundary Phase 101's Schema/Injection Hardening
// item names and Phase 101b left untouched: JWT CLAIM CONSTRUCTION from a
// caller-supplied string.
//
// A grant's name is operator-supplied text that becomes a claim field
// inside a signed token, and the token is then parsed back by a completely
// different piece of code (nkeys' decorated-JWT reader) before a server
// ever sees it. The property asserted is total and end to end: whatever
// the name, Issue either returns a clean Go error or returns a credential
// whose JWT and whose seed both parse back out of the .creds body it
// produced. There is deliberately no allowance branch, because the failure
// this guards against is a name that produces a credential the platform's
// own reader cannot read.
//
// Seeded with the shapes that break text-into-token boundaries elsewhere
// in this repository: a NATS wildcard, a newline (the .creds format is
// line-oriented and a name reaching a line boundary is the injection to
// beat), the armor delimiters of the .creds format itself, and a name long
// enough to matter.
func FuzzIssue(f *testing.F) {
	f.Add("runner-1", int64(time.Hour))
	f.Add("", int64(time.Hour))
	f.Add(">", int64(time.Minute))
	f.Add("*", int64(time.Minute))
	f.Add("a\nb", int64(time.Hour))
	f.Add("-----BEGIN NATS USER JWT-----", int64(time.Hour))
	f.Add("------END USER NKEY SEED------", int64(time.Hour))
	f.Add(strings.Repeat("n", 4096), int64(time.Hour))
	f.Add("\x00\xff", int64(time.Hour))

	op, err := meshid.NewOperator("fuzz-op")
	if err != nil {
		f.Fatalf("minting the operator: %v", err)
	}
	acct, err := meshid.NewAccount(op, "fuzz-acct")
	if err != nil {
		f.Fatalf("minting the account: %v", err)
	}
	issuer, err := meshid.NewIssuer(acct.Subject, acct.SigningKeySeed)
	if err != nil {
		f.Fatalf("building the issuer: %v", err)
	}

	f.Fuzz(func(t *testing.T, name string, expiryNanos int64) {
		const maxExpiry = int64(24 * time.Hour)
		if expiryNanos > maxExpiry {
			expiryNanos = maxExpiry
		}
		if expiryNanos <= 0 {
			expiryNanos = int64(time.Minute)
		}

		cred, err := issuer.Issue(meshid.FleetRunnerGrant(name), time.Duration(expiryNanos))
		if err != nil {
			// A clean refusal is a legitimate outcome; a panic or an
			// unreadable credential is not.
			return
		}

		// The credential has to survive the two readers the real dial
		// path uses. topology.credentialOption calls exactly these.
		if _, err := nkeys.ParseDecoratedJWT(cred.Creds); err != nil {
			t.Fatalf("Issue(%q) produced a .creds body whose JWT cannot be parsed back: %v", name, err)
		}
		kp, err := nkeys.ParseDecoratedUserNKey(cred.Creds)
		if err != nil {
			t.Fatalf("Issue(%q) produced a .creds body whose seed cannot be parsed back: %v", name, err)
		}
		if _, err := kp.Sign([]byte("nonce")); err != nil {
			t.Fatalf("Issue(%q) produced a key pair that cannot sign a nonce: %v", name, err)
		}
		if cred.Subject == "" {
			t.Fatalf("Issue(%q) returned an empty subject", name)
		}
		if !cred.Expires.After(time.Now()) {
			t.Fatalf("Issue(%q) returned an already-expired credential at %v", name, cred.Expires)
		}
	})
}
