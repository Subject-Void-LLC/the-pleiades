package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// testIssuer and testAudience are the fixed issuer/audience every test in
// this package builds tokens and evaluators against, so a token generated
// by generateTestToken always matches what newTestEvaluator pins.
const (
	testIssuer   = "pleiades-test-issuer"
	testAudience = "pleiades-test-audience"
)

// newTestEvaluator builds an Evaluator backed by a static HMAC KeyProvider
// over secret, pinned to testIssuer/testAudience. t.Fatal on any
// construction error: every secret this package's tests pass is expected
// to be valid (>= 32 bytes), so a construction failure here is a test bug,
// not a case under test (those live in jwt_hardening_test.go).
func newTestEvaluator(t testing.TB, secret []byte) auth.Evaluator {
	t.Helper()
	provider, err := auth.NewStaticKeyProvider(secret)
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}
	return eval
}

func generateTestToken(secret []byte, role string, scopes []string, exp time.Duration) string {
	claims := jwt.MapClaims{
		"sub":    "test-user",
		"role":   role,
		"scopes": scopes,
		"iss":    testIssuer,
		"aud":    testAudience,
		"exp":    time.Now().Add(exp).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString(secret)
	return signed
}

func TestJWTEvaluator_ReleaseGate(t *testing.T) {
	secret := []byte("super-secret-test-key-that-is-long-enough")
	eval := newTestEvaluator(t, secret)
	ctx := context.Background()

	// 1. Create a token with ONLY inventory:read scope
	readToken := generateTestToken(secret, "viewer", []string{"inventory:read"}, time.Hour)

	// Validate it
	id, err := eval.ValidateToken(ctx, readToken)
	if err != nil {
		t.Fatalf("failed to validate valid token: %v", err)
	}

	// 2. CheckAccess for inventory:read (Should Succeed)
	if err := eval.CheckAccess(ctx, id, "inventory:read"); err != nil {
		t.Errorf("expected success for inventory:read, got error: %v", err)
	}

	// 3. CheckAccess for inventory:write (Should Violently Reject)
	if err := eval.CheckAccess(ctx, id, "inventory:write"); err == nil {
		t.Errorf("expected VIOLENT REJECTION for inventory:write, but access was granted!")
	} else if err.Error() != "access denied: missing required scope 'inventory:write'" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestJWTEvaluator_AdminBypass(t *testing.T) {
	secret := []byte("super-secret-test-key-that-is-long-enough")
	eval := newTestEvaluator(t, secret)
	ctx := context.Background()

	// Admin with no scopes
	adminToken := generateTestToken(secret, "admin", []string{}, time.Hour)
	id, _ := eval.ValidateToken(ctx, adminToken)

	// Admin should bypass scope check
	if err := eval.CheckAccess(ctx, id, "runbook:execute"); err != nil {
		t.Errorf("admin was incorrectly rejected for runbook:execute: %v", err)
	}
}
