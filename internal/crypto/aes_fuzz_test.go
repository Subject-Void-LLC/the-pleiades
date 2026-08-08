package crypto_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// FuzzAESDecryption hammers the GCM Open function with garbage byte arrays.
// It proves the crypto library will gracefully return an error instead of panicking
// if someone manually modifies the postgres JSONB string or injects malicious payloads.
func FuzzAESDecryption(f *testing.F) {
	f.Add([]byte("short"))
	f.Add([]byte(strings.Repeat("a", 50)))
	f.Add([]byte(`{"unencrypted":"json"}`))

	key := []byte(strings.Repeat("k", 32))

	f.Fuzz(func(t *testing.T, ciphertext []byte) {
		svc, err := crypto.NewAESService(key)
		if err != nil {
			t.Fatalf("failed to init: %v", err)
		}

		// Since we are fuzzing with random garbage, Decrypt will essentially ALWAYS error
		// (unless the fuzzer magically guesses a valid AES-GCM 256 ciphertext+nonce+mac).
		// We just want to ensure it doesn't panic.
		_, _ = svc.Decrypt(ciphertext)
	})
}
