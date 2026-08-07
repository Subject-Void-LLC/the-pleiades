package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
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
	tampered := parts[0] + "." + parts[1] + "." + tamperSignature(t, parts[2])

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

// tamperSignature decodes a JWT's base64url signature segment, flips one
// bit of the signature itself, and re-encodes it, so the returned segment
// is guaranteed to carry different signature bytes.
//
// It replaces a helper that swapped the final *character* of the segment
// for a textually different one and claimed in its own doc comment that
// this guaranteed different decoded bytes. It did not, and the test built
// on it failed roughly one run in sixteen (FAILURE_PATTERNS.md #75). A
// 32-byte HMAC-SHA256 signature encodes to 43 base64url characters, which
// carry 258 bits, so the final character's low 2 bits are padding the
// decoder discards. A segment ending in "A" became "B", which differs only
// in those discarded bits: the decoded signature was byte-identical, the
// token was never actually tampered with, and ValidateToken accepted it
// correctly while the test read that as a forgery slipping through.
//
// Tampering with the decoded bytes rather than their encoding removes the
// whole class of question. It also asserts the change landed, because a
// forgery test that silently stops forging anything is worse than no test:
// it reports a security property it never exercised.
func tamperSignature(t *testing.T, segment string) string {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decoding signature segment %q: %v", segment, err)
	}
	if len(raw) == 0 {
		t.Fatal("signature segment decoded to zero bytes, so there is nothing to tamper with")
	}

	tampered := make([]byte, len(raw))
	copy(tampered, raw)
	tampered[len(tampered)-1] ^= 0x01

	if bytes.Equal(tampered, raw) {
		t.Fatal("tampering did not change the signature bytes")
	}
	return base64.RawURLEncoding.EncodeToString(tampered)
}

// TestTamperSignature_AlwaysChangesTheDecodedBytes is the persisted
// regression test for FAILURE_PATTERNS.md #75.
//
// The helper this replaced failed roughly one run in sixteen, silently and
// only sometimes, which is the worst shape a security test can fail in: it
// reported "a forged token was accepted" when what had actually happened
// was that the test never forged anything. Asserting the property directly,
// across every possible final signature byte, turns a probabilistic failure
// into a deterministic one.
//
// It enumerates the last byte specifically because that is where the old
// bug lived: base64url encodes a 32-byte signature into 43 characters
// carrying 258 bits, so the final character's low 2 bits are discarded on
// decode, and any "tampering" confined to them is not tampering at all.
func TestTamperSignature_AlwaysChangesTheDecodedBytes(t *testing.T) {
	for b := 0; b < 256; b++ {
		original := make([]byte, 32)
		original[31] = byte(b)
		segment := base64.RawURLEncoding.EncodeToString(original)

		tampered, err := base64.RawURLEncoding.DecodeString(tamperSignature(t, segment))
		if err != nil {
			t.Fatalf("tampered segment for final byte 0x%02x does not decode: %v", b, err)
		}
		if bytes.Equal(tampered, original) {
			t.Fatalf("tampering a signature whose final byte is 0x%02x produced identical bytes, so the forgery test built on it would assert nothing", b)
		}
		if len(tampered) != len(original) {
			t.Fatalf("tampering changed the signature length from %d to %d; the test's own premise is a length-preserving bit flip", len(original), len(tampered))
		}
	}
}
