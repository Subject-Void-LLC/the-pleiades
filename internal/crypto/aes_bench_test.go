package crypto_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/crypto"
)

// BenchmarkAESGCM throughput test. We compare this to the overhead of a
// PostgreSQL JSONB insert to ensure our envelope encryption isn't bottlenecking
// the ORM execution pipeline.
func BenchmarkAESGCM(b *testing.B) {
	key := []byte(strings.Repeat("k", 32))
	svc, err := crypto.NewAESService(key)
	if err != nil {
		b.Fatalf("failed to init: %v", err)
	}

	// 512 bytes is roughly the size of a standard AAA login token or SSH RSA key
	plaintext := []byte(strings.Repeat("a", 512))

	b.Logf("[REFERENCE] Postgres raw INSERT latency is typically ~1-2ms")

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
