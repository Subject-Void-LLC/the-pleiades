package api_test

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth/authtest"
	"github.com/golang-jwt/jwt/v5"
)

// This file is the request-path half of the audit
// internal/auth/jwt_forgery_test.go runs against auth.jwtEvaluator alone.
// IMPLEMENTATION.md's Phase 12 checklist left its own Schema/Injection
// Hardening item explicitly open on this exact gap: "this item also
// covers the middleware's own request-path boundary, which does not
// exist yet (no secured endpoint is mounted); re-audit the full request
// path, not just the evaluator, once one is." A secured endpoint exists
// now (api.RequireScope, wired through the real RouterConfig.Routes
// table), so this proves the same forged tokens are rejected by
// api.AuthMiddleware wrapping a real auth.Evaluator, not merely by the
// evaluator called directly.
func TestAuthMiddleware_RejectsForgedTokens(t *testing.T) {
	issuer := authtest.New(t, "middleware-forgery-issuer", "middleware-forgery-audience")

	var reached bool
	handler := api.AuthMiddleware(issuer.Evaluator())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	claims := func() jwt.MapClaims {
		return jwt.MapClaims{
			"sub":  "attacker",
			"role": "admin",
			"iss":  "middleware-forgery-issuer",
			"aud":  "middleware-forgery-audience",
			"exp":  time.Now().Add(time.Hour).Unix(),
		}
	}

	tests := []struct {
		name  string
		token func(t *testing.T) string
	}{
		{
			name: "alg none",
			token: func(t *testing.T) string {
				tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims())
				signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
				if err != nil {
					t.Fatalf("building alg:none token: %v", err)
				}
				return signed
			},
		},
		{
			name: "algorithm confusion (RS256 against an HMAC-only evaluator)",
			token: func(t *testing.T) string {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatalf("generating attacker RSA key: %v", err)
				}
				tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims())
				signed, err := tok.SignedString(key)
				if err != nil {
					t.Fatalf("signing RS256 token: %v", err)
				}
				return signed
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(http.MethodGet, "/secured", nil)
			req.Header.Set("Authorization", "Bearer "+tt.token(t))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("got status %d, want %d", rr.Code, http.StatusUnauthorized)
			}
			if reached {
				t.Error("handler ran despite a forged token")
			}
		})
	}
}
