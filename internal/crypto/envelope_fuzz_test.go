package crypto_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/crypto"
)

// FuzzEnvelopeDecrypt proves EnvelopeService.Decrypt never panics on any
// input, including a string with no relation to a real envelope at all: a
// database row is the only real source of this string, but this package
// must treat it as untrusted, since a corrupted or tampered row must fail
// closed exactly like a hostile one would (Phase 5's Schema/Injection
// Hardening item; this is the one genuinely new deserialization boundary
// this phase introduces).
func FuzzEnvelopeDecrypt(f *testing.F) {
	key := []byte(strings.Repeat("k", 32))
	previousKey := []byte(strings.Repeat("p", 32))
	svc, err := crypto.NewEnvelopeService(key, "v1", previousKey, "v0")
	if err != nil {
		f.Fatalf("NewEnvelopeService() error = %v", err)
	}

	valid, err := svc.Encrypt([]byte("seed plaintext"))
	if err != nil {
		f.Fatalf("Encrypt() error = %v", err)
	}
	f.Add(valid)

	// Every malformed shape envelope_test.go asserts a specific error for,
	// re-seeded here so the fuzzer starts from known-interesting inputs
	// rather than only random mutation.
	f.Add("")
	f.Add("v1$AES256GCM$onlyonefield")
	f.Add("v1$AES128CBC$d2hhdGV2ZXI=$d2hhdGV2ZXI=")
	f.Add("v99$AES256GCM$d2hhdGV2ZXI=$d2hhdGV2ZXI=")
	f.Add("v1$AES256GCM$not-valid-base64!!!$d2hhdGV2ZXI=")
	f.Add("v1$AES256GCM$d2hhdGV2ZXI=$not-valid-base64!!!")
	f.Add("$$$")
	f.Add("$$$$$$$$")
	f.Add(strings.Repeat("v1$AES256GCM$", 1000))

	f.Fuzz(func(t *testing.T, ciphertext string) {
		// Since we are fuzzing with arbitrary strings, Decrypt will
		// essentially always error (unless the fuzzer magically guesses a
		// valid version tag, algorithm tag, and both base64-encoded,
		// GCM-authenticated segments). We just want to ensure it never
		// panics, matching FuzzAESDecryption's own convention.
		_, _ = svc.Decrypt(ciphertext)
	})
}
