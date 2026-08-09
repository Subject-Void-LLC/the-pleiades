package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

func BenchmarkValidateToken_HMAC(b *testing.B) {
	secret := []byte("benchmark-secret-that-is-long-enough-for-hs256")
	eval := newTestEvaluator(b, secret)
	ctx := context.Background()

	token := generateTestToken(secret, "operator", []string{"inventory:read"}, time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := eval.ValidateToken(ctx, token)
		if err != nil {
			b.Fatalf("unexpected validation error: %v", err)
		}
	}
}

// BenchmarkValidateToken_RSA compares HMAC (a shared secret, symmetric
// verification) against RSA (Federated Identity, JWKS-fetched, asymmetric
// verification) throughput on the identical operation, the honest
// "industry alternative" baseline for this phase specifically: the cost of
// moving off a shared secret is RSA's own, well-known slower verification,
// not anything this codebase's JWKS implementation adds on top of it.
func BenchmarkValidateToken_RSA(b *testing.B) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		b.Fatalf("generating RSA key: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwksDocumentForRSAKey(b, &key.PublicKey, "bench-kid")))
	}))
	defer server.Close()

	provider := newTestJWKSProvider(b, server.URL)
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		b.Fatalf("NewJWTEvaluator: %v", err)
	}
	ctx := context.Background()

	token := generateRSATestToken(b, key, "bench-kid", "operator", []string{"inventory:read"}, time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := eval.ValidateToken(ctx, token)
		if err != nil {
			b.Fatalf("unexpected validation error: %v", err)
		}
	}
}
