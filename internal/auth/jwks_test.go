package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/goleak"
)

// Real HTTP + real crypto throughout this file (RULE 0): every test here
// serves an actual RFC 7517 JSON document over an actual httptest.Server
// and verifies an actual RS256/ES256-signed token against it. No live
// external Identity Provider is available in this environment, but nothing
// here is a mock of the JWKS format or of signature verification.

type testJWK struct {
	Kty, Kid, N, E, Crv, X, Y string
}

func rsaTestJWK(pub *rsa.PublicKey, kid string) testJWK {
	return testJWK{
		Kty: "RSA",
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func ecTestJWK(pub *ecdsa.PublicKey, crv, kid string) testJWK {
	size := (pub.Curve.Params().BitSize + 7) / 8
	return testJWK{
		Kty: "EC",
		Kid: kid,
		Crv: crv,
		X:   base64.RawURLEncoding.EncodeToString(leftPad(pub.X.Bytes(), size)),
		Y:   base64.RawURLEncoding.EncodeToString(leftPad(pub.Y.Bytes(), size)),
	}
}

func leftPad(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}
	padded := make([]byte, size)
	copy(padded[size-len(b):], b)
	return padded
}

func jwksDocument(t testing.TB, keys ...testJWK) string {
	t.Helper()
	entries := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		m := map[string]string{"kty": k.Kty, "kid": k.Kid}
		if k.N != "" {
			m["n"] = k.N
		}
		if k.E != "" {
			m["e"] = k.E
		}
		if k.Crv != "" {
			m["crv"] = k.Crv
		}
		if k.X != "" {
			m["x"] = k.X
		}
		if k.Y != "" {
			m["y"] = k.Y
		}
		entries = append(entries, m)
	}
	b, err := json.Marshal(map[string]interface{}{"keys": entries})
	if err != nil {
		t.Fatalf("marshaling JWKS document: %v", err)
	}
	return string(b)
}

func jwksDocumentForRSAKey(t testing.TB, pub *rsa.PublicKey, kid string) string {
	t.Helper()
	return jwksDocument(t, rsaTestJWK(pub, kid))
}

func generateRSATestToken(t testing.TB, key *rsa.PrivateKey, kid, role string, scopes []string, exp time.Duration) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":    "test-user",
		"role":   role,
		"scopes": scopes,
		"iss":    testIssuer,
		"aud":    testAudience,
		"exp":    time.Now().Add(exp).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("signing RSA test token: %v", err)
	}
	return signed
}

func generateECTestToken(t testing.TB, key *ecdsa.PrivateKey, kid, role string, scopes []string, exp time.Duration) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":    "test-user",
		"role":   role,
		"scopes": scopes,
		"iss":    testIssuer,
		"aud":    testAudience,
		"exp":    time.Now().Add(exp).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("signing EC test token: %v", err)
	}
	return signed
}

func jwksTestServer(t testing.TB, doc string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(server.Close)
	return server
}

// newTestJWKSProvider constructs a real KeyProvider and registers its
// Close for cleanup, so the background refresh goroutine and any idle HTTP
// connections it holds never outlive the test (goleak.VerifyNone in
// TestJWKSKeyProvider_Close_StopsBackgroundRefreshWithNoLeak checks the
// whole process, so every other test in this package leaking one of these
// would fail that test, not just leave its own resources dangling).
func newTestJWKSProvider(t testing.TB, url string, opts ...auth.JWKSOption) auth.KeyProvider {
	t.Helper()
	provider, err := auth.NewJWKSKeyProvider(url, opts...)
	if err != nil {
		t.Fatalf("NewJWKSKeyProvider: %v", err)
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	return provider
}

func TestJWKSKeyProvider_RealServer_RSA_VerifiesToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	server := jwksTestServer(t, jwksDocumentForRSAKey(t, &key.PublicKey, "rsa-kid"))

	provider := newTestJWKSProvider(t, server.URL)
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}

	token := generateRSATestToken(t, key, "rsa-kid", "operator", []string{"inventory:read"}, time.Hour)
	id, err := eval.ValidateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("expected real RSA-signed token to validate against the real JWKS server, got: %v", err)
	}
	if id.Subject != "test-user" || id.Role != "operator" {
		t.Errorf("unexpected identity: %+v", id)
	}
}

func TestJWKSKeyProvider_RealServer_EC_VerifiesToken(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}
	server := jwksTestServer(t, jwksDocument(t, ecTestJWK(&key.PublicKey, "P-256", "ec-kid")))

	provider := newTestJWKSProvider(t, server.URL)
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}

	token := generateECTestToken(t, key, "ec-kid", "viewer", []string{"inventory:read"}, time.Hour)
	if _, err := eval.ValidateToken(context.Background(), token); err != nil {
		t.Fatalf("expected real EC-signed token to validate against the real JWKS server, got: %v", err)
	}
}

func TestJWKSKeyProvider_RejectsTokenSignedByUnrelatedKey(t *testing.T) {
	published, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating published RSA key: %v", err)
	}
	attacker, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating attacker RSA key: %v", err)
	}
	server := jwksTestServer(t, jwksDocumentForRSAKey(t, &published.PublicKey, "rsa-kid"))

	provider := newTestJWKSProvider(t, server.URL)
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}

	// Signed with the same kid header, but a different private key: a
	// forged token, not a legitimate one that happens to reuse an ID.
	token := generateRSATestToken(t, attacker, "rsa-kid", "admin", nil, time.Hour)
	if _, err := eval.ValidateToken(context.Background(), token); err == nil {
		t.Fatal("expected a token signed by a key absent from the JWKS document to be rejected")
	}
}

func TestJWKSKeyProvider_KeyRotation_RefetchesOnUnknownKid(t *testing.T) {
	key1, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key1: %v", err)
	}
	key2, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key2: %v", err)
	}

	var mu sync.Mutex
	doc := jwksDocumentForRSAKey(t, &key1.PublicKey, "kid-1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		current := doc
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(current))
	}))
	t.Cleanup(server.Close)

	// Background refresh disabled: the only way key-2 can ever be found is
	// the bounded, on-demand refetch inside Key() itself.
	provider := newTestJWKSProvider(t, server.URL, auth.WithRefreshInterval(0))
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}
	ctx := context.Background()

	token1 := generateRSATestToken(t, key1, "kid-1", "viewer", nil, time.Hour)
	if _, err := eval.ValidateToken(ctx, token1); err != nil {
		t.Fatalf("expected kid-1 token to validate before rotation: %v", err)
	}

	// Rotate: the IdP now publishes both keys, the real-world overlap
	// window between "new key published" and "old key retired."
	mu.Lock()
	doc = jwksDocument(t, rsaTestJWK(&key1.PublicKey, "kid-1"), rsaTestJWK(&key2.PublicKey, "kid-2"))
	mu.Unlock()

	token2 := generateRSATestToken(t, key2, "kid-2", "viewer", nil, time.Hour)
	if _, err := eval.ValidateToken(ctx, token2); err != nil {
		t.Fatalf("expected kid-2 token to validate after the unknown-kid refetch: %v", err)
	}
}

func TestJWKSKeyProvider_RefreshFailureKeepsServingStaleGoodKeys(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	var fail atomic.Bool
	doc := jwksDocumentForRSAKey(t, &key.PublicKey, "rsa-kid")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(server.Close)

	provider := newTestJWKSProvider(t, server.URL, auth.WithRefreshInterval(0))
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}
	ctx := context.Background()

	token := generateRSATestToken(t, key, "rsa-kid", "viewer", nil, time.Hour)
	if _, err := eval.ValidateToken(ctx, token); err != nil {
		t.Fatalf("expected known-kid token to validate before the outage: %v", err)
	}

	// The IdP starts failing. A token with the already-cached kid must
	// still validate from the stale-but-good cache: Key never needs to
	// refetch for a kid it already has.
	fail.Store(true)
	if _, err := eval.ValidateToken(ctx, token); err != nil {
		t.Fatalf("expected already-cached kid to keep validating during an IdP outage, got: %v", err)
	}
}

func TestJWKSKeyProvider_ConstructionFailsClosed(t *testing.T) {
	validKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(unreachable.Close)

	malformedJSON := jwksTestServer(t, "{not json")
	emptyKeys := jwksTestServer(t, jwksDocument(t))
	unsupportedKty := jwksTestServer(t, jwksDocument(t, testJWK{Kty: "oct", Kid: "k1"}))

	cases := []struct {
		name string
		url  string
	}{
		{"empty URL", ""},
		{"non-http scheme", "file:///etc/passwd"},
		{"unreachable/erroring server", unreachable.URL},
		{"malformed JSON body", malformedJSON.URL},
		{"zero usable keys", emptyKeys.URL},
		{"only unsupported key types", unsupportedKty.URL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := auth.NewJWKSKeyProvider(tc.url); err == nil {
				t.Errorf("expected construction to fail closed for %s", tc.name)
			}
		})
	}

	// Control: the same construction succeeds against a genuinely good
	// document, proving the failures above are real, not a broken control.
	good := jwksTestServer(t, jwksDocumentForRSAKey(t, &validKey.PublicKey, "k1"))
	newTestJWKSProvider(t, good.URL)
}

func TestJWKSKeyProvider_Close_StopsBackgroundRefreshWithNoLeak(t *testing.T) {
	// goleak.VerifyNone must run after every other resource in this test is
	// torn down, including the httptest.Server below. t.Cleanup callbacks
	// (which jwksTestServer registers) only run after the test function and
	// all its own defers return, i.e. after a deferred VerifyNone would
	// already have checked, so this test closes its server with a plain
	// defer instead, ordered (LIFO) to run before VerifyNone.
	// Against a snapshot taken now, so this test answers only for the
	// goroutines it starts (FAILURE_PATTERNS 423).
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwksDocumentForRSAKey(t, &key.PublicKey, "k1")))
	}))
	defer server.Close()

	provider, err := auth.NewJWKSKeyProvider(server.URL, auth.WithRefreshInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("NewJWKSKeyProvider: %v", err)
	}
	// Let the background loop actually tick at least once before closing,
	// so this proves a running goroutine stops, not one that never started.
	time.Sleep(30 * time.Millisecond)

	closer, ok := provider.(interface{ Close() error })
	if !ok {
		t.Fatal("expected the JWKS provider to expose Close()")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Idempotent: a second Close must not panic.
	if err := closer.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
