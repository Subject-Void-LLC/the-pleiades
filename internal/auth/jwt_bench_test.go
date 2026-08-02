package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

func BenchmarkValidateToken(b *testing.B) {
	secret := []byte("benchmark-secret")
	eval := auth.NewJWTEvaluator(secret)
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
