package crypto_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/crypto"
)

// BenchmarkAESGCM throughput test for single-key AES-256-GCM. This is the
// real, same-host, same-run baseline envelope_bench_test.go's
// BenchmarkEnvelopeEncrypt/Decrypt compare against to report the DEK/KEK
// indirection's real measured overhead, rather than an external, uncited
// figure (Postgres INSERT latency measures a different system entirely
// and was never locally measured here; removed for that reason).
func BenchmarkAESGCM(b *testing.B) {
	key := []byte(strings.Repeat("k", 32))
	svc, err := crypto.NewAESService(key)
	if err != nil {
		b.Fatalf("failed to init: %v", err)
	}

	// 512 bytes is roughly the size of a standard AAA login token or SSH RSA key
	plaintext := []byte(strings.Repeat("a", 512))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ciphertext, err := svc.Encrypt(plaintext)
		if err != nil {
			b.Fatalf("failed to encrypt: %v", err)
		}

		_, err = svc.Decrypt(ciphertext)
		if err != nil {
			b.Fatalf("failed to decrypt: %v", err)
		}
	}
}
