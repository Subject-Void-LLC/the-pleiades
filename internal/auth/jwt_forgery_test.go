package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// This file is the persisted regression test IMPLEMENTATION.md's Phase 39
// (Schema & Injection Hardening) and Phase 12 (Zero-Trust Middleware) both
// describe as already run: "five real forged tokens... were all correctly
// rejected by the real ValidateToken." Neither phase's own text was
// backed by a test file anywhere in this repository (grepped for
// "SigningMethodNone", "alg.*none", "Tamper", "Stripped": zero hits before
// this file), so the claim was true only as long as nobody re-ran it. This
// closes that gap: the same five forgeries, plus a stripped-signature
// sixth, run for real against auth.jwtEvaluator.ValidateToken, with a
// regression test to catch the next one silently disappearing too.
//
// "Real" here means a real jwt.NewWithClaims call and a real
// (non-)signature, never a hand-built string pretending to be a token:
// forging a JWT is exactly the shape of input this suite has to get
// right, so building one wrong would prove nothing.

const forgeryTestIssuer = "forgery-test-issuer"
const forgeryTestAudience = "forgery-test-audience"

// forgedClaims returns a baseline claim set an attacker would want
// accepted: an admin role and the wildcard scope. Every test below forges
// the signature or timing around this same claim body, never the claims
// themselves, since the claims are never the security boundary here.
func forgedClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub":    "attacker",
		"role":   "admin",
		"scopes": []string{"*"},
		"iss":    forgeryTestIssuer,
		"aud":    forgeryTestAudience,
		"exp":    time.Now().Add(time.Hour).Unix(),
	}
}

// TestValidateToken_RejectsAlgNone forges a token with the "none"
// algorithm and no signature at all, the classic JWT library
// misconfiguration bug (CVE-2015-9235 and its many descendants): a
// validator that trusts the token's own "alg" header instead of pinning
// one accepts this as a valid, unsigned admin token.
func TestValidateToken_RejectsAlgNone(t *testing.T) {
	secret := []byte("alg-none-forgery-test-secret-32-bytes-minimum")
	eval := newTestEvaluator(t, secret)

	token := jwt.NewWithClaims(jwt.SigningMethodNone, forgedClaims())
	forged, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building alg:none token: %v", err)
	}

	if _, err := eval.ValidateToken(context.Background(), forged); err == nil {
		t.Fatal("expected an alg:none token to be rejected, got a validated identity")
	}
}

// TestValidateToken_RejectsAlgorithmConfusion forges a token signed with a
// real RSA private key nobody but the attacker holds, then presents it to
// an evaluator backed by auth.NewStaticKeyProvider (HS256 only). A
// validator that does not pin the algorithm family can be tricked into
// treating the RSA *public* key (often distributable, sometimes even the
// HMAC secret itself under a confusion attack) as an HMAC secret; jwt.
// WithValidMethods(keyProvider.Algorithms()) is what this test proves
// still closes that door.
func TestValidateToken_RejectsAlgorithmConfusion(t *testing.T) {
	secret := []byte("algorithm-confusion-forgery-test-secret-ok")
	eval := newTestEvaluator(t, secret)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating attacker RSA key: %v", err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, forgedClaims())
	forged, err := token.SignedString(rsaKey)
	if err != nil {
		t.Fatalf("signing RS256 token: %v", err)
	}

	if _, err := eval.ValidateToken(context.Background(), forged); err == nil {
		t.Fatal("expected an RS256 token to be rejected by an HMAC-only evaluator, got a validated identity")
	}
}

// TestValidateToken_RejectsExpiredToken reuses generateTestToken
// (jwt_test.go) with a negative TTL, so the token's own "exp" claim is
// already in the past.
func TestValidateToken_RejectsExpiredToken(t *testing.T) {
	secret := []byte("expired-token-forgery-test-secret-32-bytes-ok")
	eval := newTestEvaluator(t, secret)

	expired := generateTestToken(secret, "admin", []string{"*"}, -time.Hour)

	if _, err := eval.ValidateToken(context.Background(), expired); err == nil {
		t.Fatal("expected an expired token to be rejected, got a validated identity")
	}
}

// TestValidateToken_RejectsNotYetValidToken forges a real, correctly
// signed token whose "nbf" (not-before) claim is an hour in the future.
// This is the check jwt.ParseWithClaims's default Validator performs
// whenever the claim is present (NewJWTEvaluator adds no option for it,
// unlike WithExpirationRequired, precisely because the library default
// already covers it), and it is exercised here directly rather than
// merely cited.
func TestValidateToken_RejectsNotYetValidToken(t *testing.T) {
	secret := []byte("not-yet-valid-forgery-test-secret-32-bytes-ok")
	eval := newTestEvaluator(t, secret)

	claims := forgedClaims()
	claims["nbf"] = time.Now().Add(time.Hour).Unix()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	forged, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("signing not-yet-valid token: %v", err)
	}

	if _, err := eval.ValidateToken(context.Background(), forged); err == nil {
		t.Fatal("expected a not-yet-valid token to be rejected, got a validated identity")
	}
}

// TestValidateToken_RejectsTamperedSignature takes a real, validly signed
// token and flips one byte inside its signature segment, the shape of
// forgery a length-preserving bit flip in transit or storage would
// produce.
func TestValidateToken_RejectsTamperedSignature(t *testing.T) {
	secret := []byte("tampered-signature-forgery-test-secret-32-ok")
	eval := newTestEvaluator(t, secret)

	valid := generateTestToken(secret, "admin", []string{"*"}, time.Hour)
	parts := strings.Split(valid, ".")
	if len(parts) != 3 {
		t.Fatalf("expected a 3-segment JWT, got %d segments", len(parts))
	}
	tampered := parts[0] + "." + parts[1] + "." + flipLastChar(parts[2])

	if _, err := eval.ValidateToken(context.Background(), tampered); err == nil {
		t.Fatal("expected a tampered signature to be rejected, got a validated identity")
	}
}

// TestValidateToken_RejectsStrippedSignature takes a real, validly signed
// token and removes its signature segment entirely, the forgery an
// attacker gets for free by truncating a token at the second ".": the
// resulting string decodes to the identical header and claims with no
// signature to check at all.
func TestValidateToken_RejectsStrippedSignature(t *testing.T) {
	secret := []byte("stripped-signature-forgery-test-secret-32-ok")
	eval := newTestEvaluator(t, secret)

	valid := generateTestToken(secret, "admin", []string{"*"}, time.Hour)
	parts := strings.Split(valid, ".")
	if len(parts) != 3 {
		t.Fatalf("expected a 3-segment JWT, got %d segments", len(parts))
	}
	stripped := parts[0] + "." + parts[1] + "."

	if _, err := eval.ValidateToken(context.Background(), stripped); err == nil {
		t.Fatal("expected a stripped signature to be rejected, got a validated identity")
	}
}

// flipLastChar swaps the final character of s for a different one from
// the base64url alphabet, so the returned string is guaranteed to decode
// to different bytes rather than risk picking the same character back by
// chance.
func flipLastChar(s string) string {
	if s == "" {
		return s
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	last := s[len(s)-1]
	for _, c := range alphabet {
		if byte(c) != last {
			return s[:len(s)-1] + string(c)
		}
	}
	return s
}
