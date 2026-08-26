package meshid_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
)

// BenchmarkIssue measures what minting one credential costs, which is the
// number that decides whether issuance can sit anywhere near a hot path.
//
// It is the evidence behind a design decision already taken rather than a
// curiosity: Phase 101b set DefaultUserExpiry to 12 hours and refused to
// mint per job, and Phase 101c keeps that refusal. Ed25519 signing is
// cheap enough that "it would be slow" was never the argument; the
// argument is coupling, and this benchmark is what stops someone
// relitigating it on performance grounds.
func BenchmarkIssue(b *testing.B) {
	op, err := meshid.NewOperator("bench-op")
	if err != nil {
		b.Fatal(err)
	}
	acct, err := meshid.NewAccount(op, "bench-acct")
	if err != nil {
		b.Fatal(err)
	}
	issuer, err := meshid.NewIssuer(acct.Subject, acct.SigningKeySeed)
	if err != nil {
		b.Fatal(err)
	}
	grant := meshid.FleetRunnerGrant("bench-runner")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := issuer.Issue(grant, time.Hour); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNewAccount measures the other half, minting an account, which
// happens once per deployment rather than once per Runner.
func BenchmarkNewAccount(b *testing.B) {
	op, err := meshid.NewOperator("bench-op")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := meshid.NewAccount(op, "bench-acct"); err != nil {
			b.Fatal(err)
		}
	}
}
