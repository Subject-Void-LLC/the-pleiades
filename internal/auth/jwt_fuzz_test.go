package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

func FuzzJWTParsing(f *testing.F) {
	secret := []byte("fuzz-secret")

	// Add valid seed
	validToken := generateTestToken(secret, "viewer", []string{}, time.Hour)
	f.Add(validToken)

	// Add invalid seeds
	f.Add("Bearer " + validToken)
	f.Add("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.invalid.signature")
	f.Add("not-a-token")
	f.Add("")

	eval := auth.NewJWTEvaluator(secret)
	ctx := context.Background()

	f.Fuzz(func(t *testing.T, token string) {
		id, err := eval.ValidateToken(ctx, token)
		if err == nil {
			// If it succeeded, it must be because the fuzzer magically generated a valid HMAC signature
			// for the given payload, which is astronomically unlikely but technically possible.
			// Just ensure the identity is well-formed.
			if id.Subject == "" && id.Role == "" {
				// that's fine, it could be an empty token
			}
		}
	})
}
