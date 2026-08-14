// Benchmarks for the KDF.
//
// These are not decoration. IMPLEMENTATION.md's checkbox rule 2 requires a
// benchmark that actually ran with results recorded, and the Argon2id
// parameters in kdf.go are not defensible without two numbers: what one
// verification costs in wall time, and what it costs in memory. The second
// is what bounds how many concurrent logins an unauthenticated endpoint can
// be allowed to start, which is a real deployment decision rather than a
// curiosity.
package localauth_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
)

// BenchmarkHash measures deriving a new credential, which happens on a
// password set and on a rehash-on-login upgrade.
func BenchmarkHash(b *testing.B) {
	for b.Loop() {
		if _, err := localauth.Hash("a-representative-password"); err != nil {
			b.Fatalf("Hash() error = %v", err)
		}
	}
}

// BenchmarkVerify measures the hot path: one login attempt, successful or
// not. Every failed guess costs the same, which is the point of the
// parameters and the reason an attacker cannot grind cheaply.
//
// B/op here is the number the concurrency bound is derived from. At roughly
// 19 MiB per call, a controller that also holds the ent pool, the NATS
// connection and the executor cannot afford unbounded parallel logins on an
// unauthenticated endpoint.
func BenchmarkVerify(b *testing.B) {
	const password = "a-representative-password"
	encoded, err := localauth.Hash(password)
	if err != nil {
		b.Fatalf("Hash() error = %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		ok, err := localauth.Verify(encoded, password)
		if err != nil || !ok {
			b.Fatalf("Verify() = %v, %v", ok, err)
		}
	}
}

// BenchmarkVerify_WrongPassword confirms a wrong guess is not cheaper than
// a right one.
//
// If it were, the difference would be measurable from outside and the login
// endpoint would leak whether a guess was close, which is a stronger oracle
// than account existence.
func BenchmarkVerify_WrongPassword(b *testing.B) {
	encoded, err := localauth.Hash("a-representative-password")
	if err != nil {
		b.Fatalf("Hash() error = %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if ok, _ := localauth.Verify(encoded, "the-wrong-password-here"); ok {
			b.Fatal("Verify() = true for the wrong password")
		}
	}
}

// BenchmarkDecode isolates the parser from the derivation.
//
// It should be orders of magnitude cheaper than a verification. If it ever
// is not, something in the parse path is doing work it should not, and the
// parse runs before any of the bounds checks have been applied.
func BenchmarkDecode(b *testing.B) {
	encoded, err := localauth.Hash("a-representative-password")
	if err != nil {
		b.Fatalf("Hash() error = %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, _, _, err := localauth.Decode(encoded); err != nil {
			b.Fatalf("Decode() error = %v", err)
		}
	}
}
