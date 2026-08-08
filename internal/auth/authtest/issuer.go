// Package authtest is a test-only token issuer, built to close the gap
// api.IdentityKeyForTest left open: that constant let any caller, test or
// not, inject an *auth.Identity straight into a request context, bypassing
// AuthMiddleware entirely rather than exercising it. HANDOFF_DOCUMENT.md's
// Phase 11 session named the fix this package is: "the real fix is a
// test-only token issuer Phase 12 should build."
//
// Issuer signs real tokens against a real, randomly generated HMAC secret
// and hands back the matching auth.Evaluator, so a caller obtains a token
// AuthMiddleware really validates through the same auth.NewJWTEvaluator /
// auth.NewStaticKeyProvider path a production process uses, rather than a
// fabricated identity nothing checks. It cannot grant a scope or role
// AuthMiddleware would not itself have accepted from a real caller.
//
// This package is not gated behind a Go build tag (a _test.go file could
// not be imported across package boundaries, which is exactly the
// tests/e2e problem this package exists to solve; see IdentityKeyForTest's
// old doc comment for why moving it into export_test.go alone was not
// enough). internal/archtest's own TestAuthtestNeverImportedByProductionCode
// enforces the boundary a build tag would otherwise give up: no
// production package may import this one.
package authtest

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// secretLen matches auth.NewStaticKeyProvider's own HS256 minimum
// (RFC 7518 SS3.2), so an Issuer's secret is never itself the reason a
// real code path would have rejected it.
const secretLen = 32

// defaultTTL is how far in the future a token minted by Issue expires. It
// is generous enough that no test using it should ever observe a spurious
// expiry, while still being a real, finite value: an Issuer that minted
// tokens with no expiry could not prove jwt.WithExpirationRequired's own
// behavior even by accident.
const defaultTTL = time.Hour

// Issuer mints tokens for one fixed issuer/audience pair, signed against
// one fixed, randomly generated secret, and exposes the auth.Evaluator
// that verifies them.
type Issuer struct {
	secret    []byte
	issuer    string
	audience  string
	evaluator auth.Evaluator
}

// New builds an Issuer with a fresh random secret and the given
// issuer/audience. t.Fatal on any construction error: every input this
// constructor builds is valid by construction, so a failure here is a bug
// in this package, not a case under test.
func New(t testing.TB, issuer, audience string) *Issuer {
	t.Helper()

	secret := make([]byte, secretLen)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("authtest: generating random secret: %v", err)
	}

	provider, err := auth.NewStaticKeyProvider(secret)
	if err != nil {
		t.Fatalf("authtest: NewStaticKeyProvider: %v", err)
	}
	evaluator, err := auth.NewJWTEvaluator(provider, issuer, audience)
	if err != nil {
		t.Fatalf("authtest: NewJWTEvaluator: %v", err)
	}

	return &Issuer{secret: secret, issuer: issuer, audience: audience, evaluator: evaluator}
}

// Evaluator returns the auth.Evaluator that verifies tokens Issue mints.
// Pass this to api.AuthMiddleware in place of a production evaluator.
func (i *Issuer) Evaluator() auth.Evaluator {
	return i.evaluator
}

// Token mints a signed HS256 token for id, valid for defaultTTL. t.Fatal
// on a signing error, for the same reason New does: nothing about a
// well-formed *auth.Identity should be able to make signing fail.
func (i *Issuer) Token(t testing.TB, id *auth.Identity) string {
	t.Helper()

	scopes := make([]string, len(id.Scopes))
	for idx, s := range id.Scopes {
		scopes[idx] = string(s)
	}

	claims := jwt.MapClaims{
		"sub":    id.Subject,
		"role":   string(id.Role),
		"scopes": scopes,
		"iss":    i.issuer,
		"aud":    i.audience,
		"exp":    time.Now().Add(defaultTTL).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	if err != nil {
		t.Fatalf("authtest: signing token: %v", err)
	}
	return token
}

// BearerToken mints a token for id (see Token) and returns it with the
// "Bearer " scheme prefix already applied, ready for an Authorization
// header.
func (i *Issuer) BearerToken(t testing.TB, id *auth.Identity) string {
	t.Helper()
	return "Bearer " + i.Token(t, id)
}
