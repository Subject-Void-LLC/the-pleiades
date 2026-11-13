// These tests hold the JWKS provider to three promises an anonymous caller
// or an identity provider outage would otherwise break: a request cannot
// choose how often this Controller fetches the key set (FAILURE_PATTERNS
// 1227), the key set has a maximum age and a failed refresh is reported
// (1228), and the key set is never fetched over plain HTTP (1229). Every one
// runs a real TLS server and verifies real signatures, as jwks_test.go does.
package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// newFakeClock starts a clock at a fixed instant.
func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)}
}

// Now reads the clock.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// countingIdP is a JWKS endpoint that counts its fetches and can be told to
// fail, to publish another document, or to hold each answer for a while.
type countingIdP struct {
	server  *httptest.Server
	fetches atomic.Int64

	mu    sync.Mutex
	doc   string
	fail  bool
	delay time.Duration
}

// newCountingIdP serves doc over TLS until the test ends.
func newCountingIdP(t *testing.T, doc string) *countingIdP {
	t.Helper()
	idp := &countingIdP{doc: doc}
	idp.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idp.fetches.Add(1)
		idp.mu.Lock()
		doc, fail, delay := idp.doc, idp.fail, idp.delay
		idp.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(idp.server.Close)
	return idp
}

// set changes what the endpoint answers from now on.
func (idp *countingIdP) set(doc string, fail bool, delay time.Duration) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	idp.doc, idp.fail, idp.delay = doc, fail, delay
}

// newRSAKey generates a 2048 bit key or fails the test.
func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	return key
}

// TestJWKSKeyProvider_AnEmptyKidNeverFetches: a token with no kid can name
// no key this provider holds, so it is refused without asking the IdP, by
// the provider and through the evaluator alike.
func TestJWKSKeyProvider_AnEmptyKidNeverFetches(t *testing.T) {
	key := newRSAKey(t)
	idp := newCountingIdP(t, jwksDocumentForRSAKey(t, &key.PublicKey, "k1"))
	provider := newTestJWKSProvider(t, idp.server, auth.WithRefreshInterval(0))
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}
	afterStart := idp.fetches.Load()

	for i := 0; i < 20; i++ {
		if _, err := provider.Key(context.Background(), ""); err == nil {
			t.Fatal("an empty kid returned a key")
		}
	}
	// A real token whose header carries no kid at all.
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "x", "iss": testIssuer, "aud": testAudience, "exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	if _, err := eval.ValidateToken(context.Background(), signed); err == nil {
		t.Fatal("a token with no kid validated")
	}

	if got := idp.fetches.Load() - afterStart; got != 0 {
		t.Errorf("tokens with no kid caused %d fetches from the IdP, want 0", got)
	}
}

// TestJWKSKeyProvider_UnknownKidsWaitOutACooldown: after a request-path
// fetch, an unknown kid causes no other fetch until the cooldown has
// passed, however many requests name one. This is the bound across
// requests that FAILURE_PATTERNS 1227 says the old code lacked: there,
// each of these calls was its own fetch.
func TestJWKSKeyProvider_UnknownKidsWaitOutACooldown(t *testing.T) {
	key := newRSAKey(t)
	idp := newCountingIdP(t, jwksDocumentForRSAKey(t, &key.PublicKey, "k1"))
	clock := newFakeClock()
	provider := newTestJWKSProvider(t, idp.server, auth.WithRefreshInterval(0), auth.WithClockForTest(clock.Now))
	afterStart := idp.fetches.Load()

	if _, err := provider.Key(context.Background(), "garbage-1"); err == nil {
		t.Fatal("an unknown kid returned a key")
	}
	if got := idp.fetches.Load() - afterStart; got != 1 {
		t.Fatalf("the first unknown kid caused %d fetches, want 1", got)
	}

	for i := 0; i < 50; i++ {
		clock.Advance(time.Second / 2)
		if _, err := provider.Key(context.Background(), "garbage-again"); err == nil {
			t.Fatal("an unknown kid returned a key")
		}
	}
	if got := idp.fetches.Load() - afterStart; got != 1 {
		t.Fatalf("50 unknown kids inside the cooldown caused %d fetches in all, want still 1", got)
	}

	// Past the cooldown, a request may fetch again: a real rotation is late
	// by at most the cooldown, never refused for good.
	clock.Advance(time.Minute)
	if _, err := provider.Key(context.Background(), "garbage-3"); err == nil {
		t.Fatal("an unknown kid returned a key")
	}
	if got := idp.fetches.Load() - afterStart; got != 2 {
		t.Errorf("an unknown kid after the cooldown caused %d fetches in all, want 2", got)
	}
}

// TestJWKSKeyProvider_ConcurrentUnknownKidsShareOneFetch: many requests
// arriving together with a kid the cache lacks wait on one shared fetch,
// and every one of them gets the rotated key from it. A cooldown alone
// would hold the count at one too, but would refuse every request after the
// first while the fetch was still running; only a shared fetch lets them all
// through.
func TestJWKSKeyProvider_ConcurrentUnknownKidsShareOneFetch(t *testing.T) {
	oldKey, newKey := newRSAKey(t), newRSAKey(t)
	idp := newCountingIdP(t, jwksDocumentForRSAKey(t, &oldKey.PublicKey, "old"))
	provider := newTestJWKSProvider(t, idp.server, auth.WithRefreshInterval(0))
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}
	afterStart := idp.fetches.Load()

	// The IdP rotates, and answers slowly enough that every request below
	// arrives while the one fetch is still running.
	idp.set(jwksDocument(t, rsaTestJWK(&oldKey.PublicKey, "old"), rsaTestJWK(&newKey.PublicKey, "new")), false, 300*time.Millisecond)
	token := generateRSATestToken(t, newKey, "new", "viewer", nil, time.Hour)

	const callers = 50
	var wg sync.WaitGroup
	var refused atomic.Int64
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := eval.ValidateToken(context.Background(), token); err != nil {
				refused.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := idp.fetches.Load() - afterStart; got != 1 {
		t.Errorf("%d concurrent requests naming one new kid caused %d fetches, want 1", callers, got)
	}
	if n := refused.Load(); n != 0 {
		t.Errorf("%d of %d requests carrying the rotated key were refused, want none", n, callers)
	}
}

// TestJWKSKeyProvider_AWaitingRequestLeavesOnItsOwnDeadline: a request that
// gives up while waiting on the shared fetch returns at once, and the fetch
// still completes for the others.
func TestJWKSKeyProvider_AWaitingRequestLeavesOnItsOwnDeadline(t *testing.T) {
	oldKey, newKey := newRSAKey(t), newRSAKey(t)
	idp := newCountingIdP(t, jwksDocumentForRSAKey(t, &oldKey.PublicKey, "old"))
	provider := newTestJWKSProvider(t, idp.server, auth.WithRefreshInterval(0))
	idp.set(jwksDocument(t, rsaTestJWK(&newKey.PublicKey, "new")), false, 300*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	began := time.Now()
	if _, err := provider.Key(ctx, "new"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Key under a 20ms deadline = %v, want the deadline", err)
	}
	if waited := time.Since(began); waited > 200*time.Millisecond {
		t.Errorf("a request with a 20ms deadline waited %s for a 300ms fetch", waited)
	}

	// The fetch it left kept running and landed the new key.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := provider.Key(context.Background(), "new"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the shared fetch never landed the rotated key after its first waiter left")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestJWKSKeyProvider_StaleKeysAreRefusedPastTheBound: with the IdP down,
// a cached key is trusted up to the staleness bound and refused after it,
// and a refresh that succeeds again restores it with no restart
// (FAILURE_PATTERNS 1228).
func TestJWKSKeyProvider_StaleKeysAreRefusedPastTheBound(t *testing.T) {
	key := newRSAKey(t)
	doc := jwksDocumentForRSAKey(t, &key.PublicKey, "k1")
	idp := newCountingIdP(t, doc)
	clock := newFakeClock()
	provider := newTestJWKSProvider(t, idp.server, auth.WithRefreshInterval(0), auth.WithClockForTest(clock.Now),
		auth.WithLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))))
	eval, err := auth.NewJWTEvaluator(provider, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewJWTEvaluator: %v", err)
	}
	token := generateRSATestToken(t, key, "k1", "viewer", nil, 24*time.Hour)
	ctx := context.Background()

	idp.set(doc, true, 0)
	clock.Advance(auth.DefaultJWKSMaxStaleness - time.Minute)
	if _, err := eval.ValidateToken(ctx, token); err != nil {
		t.Fatalf("a cached key inside the bound was refused: %v", err)
	}

	clock.Advance(2 * time.Minute)
	_, err = eval.ValidateToken(ctx, token)
	if err == nil {
		t.Fatal("a cached key past the 12h bound was trusted while the IdP was down")
	}
	if !strings.Contains(err.Error(), "staleness bound") {
		t.Errorf("refusal = %v, want it to name the staleness bound", err)
	}

	// The IdP answers again. Past the request cooldown, the next request
	// refreshes the set and the key is trusted again.
	idp.set(doc, false, 0)
	clock.Advance(2 * time.Minute)
	if _, err := eval.ValidateToken(ctx, token); err != nil {
		t.Fatalf("after the IdP recovered the key was still refused: %v", err)
	}
}

// TestJWKSKeyProvider_AWithdrawnKeyIsRefusedOnceTheSetRefreshes: a stale
// set that refreshes into one without the token's key refuses the key,
// rather than keeping it.
func TestJWKSKeyProvider_AWithdrawnKeyIsRefusedOnceTheSetRefreshes(t *testing.T) {
	withdrawn, kept := newRSAKey(t), newRSAKey(t)
	idp := newCountingIdP(t, jwksDocument(t, rsaTestJWK(&withdrawn.PublicKey, "gone"), rsaTestJWK(&kept.PublicKey, "kept")))
	clock := newFakeClock()
	provider := newTestJWKSProvider(t, idp.server, auth.WithRefreshInterval(0), auth.WithClockForTest(clock.Now))

	idp.set(jwksDocumentForRSAKey(t, &kept.PublicKey, "kept"), false, 0)
	clock.Advance(auth.DefaultJWKSMaxStaleness + time.Minute)
	if _, err := provider.Key(context.Background(), "gone"); err == nil {
		t.Fatal("a key the refreshed set no longer holds was returned")
	}
	if _, err := provider.Key(context.Background(), "kept"); err != nil {
		t.Fatalf("the key the refreshed set holds was refused: %v", err)
	}
}

// TestJWKSKeyProvider_ARefreshFailureIsLogged: a failed background
// refresh is written to the provider's logger with the error and the key
// set's age, where the old loop discarded it (FAILURE_PATTERNS 1228), and a
// later success says so.
func TestJWKSKeyProvider_ARefreshFailureIsLogged(t *testing.T) {
	key := newRSAKey(t)
	doc := jwksDocumentForRSAKey(t, &key.PublicKey, "k1")
	idp := newCountingIdP(t, doc)
	logs := &syncBuffer{}
	newTestJWKSProvider(t, idp.server, auth.WithRefreshInterval(10*time.Millisecond),
		auth.WithLogger(slog.New(slog.NewJSONHandler(logs, nil))))

	idp.set(doc, true, 0)
	waitForLog(t, logs, "JWKS refresh failed")
	line := logs.String()
	for _, want := range []string{`"level":"WARN"`, `"error":"fetching JWKS: unexpected status 503`, `"keys_age"`, `"max_staleness"`, `"trigger":"background"`} {
		if !strings.Contains(line, want) {
			t.Errorf("the failure log lacks %s:\n%s", want, line)
		}
	}

	idp.set(doc, false, 0)
	waitForLog(t, logs, "JWKS refresh succeeded again")
}

// TestNewJWKSKeyProvider_RefusesANonPositiveStalenessBound: there is no
// value that switches the bound off.
func TestNewJWKSKeyProvider_RefusesANonPositiveStalenessBound(t *testing.T) {
	key := newRSAKey(t)
	idp := newCountingIdP(t, jwksDocumentForRSAKey(t, &key.PublicKey, "k1"))
	for _, d := range []time.Duration{0, -time.Hour} {
		if _, err := auth.NewJWKSKeyProvider(idp.server.URL, auth.WithHTTPClient(idp.server.Client()), auth.WithMaxStaleness(d)); err == nil {
			t.Errorf("a staleness bound of %s was accepted", d)
		}
	}
}

// TestNewJWKSKeyProvider_ARefusedURLDoesNotEchoItsPassword: a URL is
// refused by its scheme alone, and a failed first fetch names the URL with
// its password removed.
func TestNewJWKSKeyProvider_ARefusedURLDoesNotEchoItsPassword(t *testing.T) {
	_, err := auth.NewJWKSKeyProvider("http://user:hunter2@idp.example/keys")
	if err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain http refusal = %v, want it to ask for https without the password", err)
	}

	down := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)
	withPassword := strings.Replace(down.URL, "https://", "https://user:hunter2@", 1)
	_, err = auth.NewJWKSKeyProvider(withPassword, auth.WithHTTPClient(down.Client()))
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("failed first fetch = %v, want an error without the password", err)
	}
}

// syncBuffer is a bytes.Buffer safe for the logger's goroutine and the
// test's to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForLog waits up to five seconds for want to appear in logs.
func waitForLog(t *testing.T, logs *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no log line containing %q within 5s; logs:\n%s", want, logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
