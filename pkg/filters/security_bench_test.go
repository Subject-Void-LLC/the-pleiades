package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// BenchmarkSHA256Hash and BenchmarkHMACGenerate measure this phase's two
// hashing functions against a realistic config-file-sized payload,
// proving the cost stays proportional to input size rather than
// exploding, the same concern MaxStructuredInputBytes' own cap exists to
// bound (see pkg/filters/validate_bench_test.go's identical reasoning
// for Phase 54's document-shaped validators).
func BenchmarkSHA256Hash(b *testing.B) {
	payload := make([]byte, 8192)
	for i := range payload {
		payload[i] = byte(i)
	}
	s := string(payload)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.SHA256Hash(s)
	}
}

func BenchmarkHMACGenerate(b *testing.B) {
	payload := make([]byte, 8192)
	for i := range payload {
		payload[i] = byte(i)
	}
	message := string(payload)
	for i := 0; i < b.N; i++ {
		filters.HMACGenerate(message, "a-shared-secret")
	}
}

// BenchmarkSecureCompare measures the constant-time comparison itself,
// separate from TestSecureCompare_ConstantTime's own statistical
// early-vs-late-diff proof: this one just establishes the raw per-call
// cost.
func BenchmarkSecureCompare(b *testing.B) {
	a := "a-secret-token-value-of-realistic-length"
	c := "a-secret-token-value-of-realistic-length"
	for i := 0; i < b.N; i++ {
		filters.SecureCompare(a, c)
	}
}

// BenchmarkGenerateRandomPassword measures the cost of crypto/rand-backed
// character selection at a realistic password length.
func BenchmarkGenerateRandomPassword(b *testing.B) {
	for i := 0; i < b.N; i++ {
		filters.GenerateRandomPassword(32)
	}
}

// BenchmarkMaskPII measures the three-pattern redaction pass against a
// realistic multi-line log excerpt, this phase's own document-shaped
// input.
func BenchmarkMaskPII(b *testing.B) {
	line := "2026-08-20T12:00:00Z INFO user 123-45-6789 charged card 4111-1111-1111-1111 " +
		"via Authorization: Bearer abcDEF123.ghiJKL456 for order 987654321\n"
	var payload string
	for i := 0; i < 50; i++ {
		payload += line
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.MaskPII(payload)
	}
}

// BenchmarkParseX509Certificate measures certificate parsing cost
// against a real, freshly generated certificate (the same fixture
// TestParseX509Certificate itself uses, via buildSelfSignedCertPEM).
func BenchmarkParseX509Certificate(b *testing.B) {
	certPEM, _, err := buildSelfSignedCertPEM()
	if err != nil {
		b.Fatalf("building self-signed test certificate: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.ParseX509Certificate(certPEM)
	}
}
