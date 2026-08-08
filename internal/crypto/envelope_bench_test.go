package crypto_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// BenchmarkEnvelopeEncrypt/BenchmarkEnvelopeDecrypt measure the real cost
// of the DEK/KEK indirection over single-key AES-256-GCM. The honest,
// same-host, same-run comparison is BenchmarkAESGCM in this same package
// (aes_bench_test.go): both use the identical 512-byte payload, so the
// delta between them is the real, measured cost of generating a fresh DEK
// per call and wrapping it under the KEK, not a cited figure for a
// different system (no external reference platform for AES-256-GCM
// envelope throughput is recorded anywhere in this repo, and fabricating
// one would repeat the exact mistake this phase's own checklist flagged
// in the prior, uncited Postgres INSERT figure this file replaces).
func BenchmarkEnvelopeEncrypt(b *testing.B) {
	key := []byte(strings.Repeat("k", 32))
	svc, err := crypto.NewEnvelopeService(key, "v1", nil, "")
	if err != nil {
		b.Fatalf("failed to init: %v", err)
	}

	plaintext := []byte(strings.Repeat("a", 512))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Encrypt(plaintext); err != nil {
			b.Fatalf("failed to encrypt: %v", err)
		}
	}
}

func BenchmarkEnvelopeDecrypt(b *testing.B) {
	key := []byte(strings.Repeat("k", 32))
	svc, err := crypto.NewEnvelopeService(key, "v1", nil, "")
	if err != nil {
		b.Fatalf("failed to init: %v", err)
	}

	plaintext := []byte(strings.Repeat("a", 512))
	ciphertext, err := svc.Encrypt(plaintext)
	if err != nil {
		b.Fatalf("failed to encrypt: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Decrypt(ciphertext); err != nil {
			b.Fatalf("failed to decrypt: %v", err)
		}
	}
}
