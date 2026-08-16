package session_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"

	_ "github.com/mattn/go-sqlite3"
)

// Every store test runs against a real in-memory SQLite database through
// the same entStore production uses, per RULE 0: the expiry predicates and
// the unique index are SQL, so a double standing in for the client would
// be testing the double.
func newStore(t *testing.T) (session.Store, *ent.Client) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	return session.NewEntStore(client), client
}

func testIdentity() *auth.Identity {
	return &auth.Identity{
		Subject: "operator@example.com",
		Role:    auth.RoleOperator,
		Scopes:  []auth.Scope{auth.ScopeInventoryRead, auth.ScopeJobRead},
	}
}

func TestNewToken_IsUniqueAndHighEntropy(t *testing.T) {
	seen := make(map[string]bool, 256)
	for range 256 {
		tok, err := session.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if seen[tok] {
			t.Fatalf("NewToken returned a duplicate: %q", tok)
		}
		seen[tok] = true
		// 32 raw bytes, base64url without padding.
		if len(tok) != 43 {
			t.Fatalf("NewToken returned %d characters, want 43 (256 bits)", len(tok))
		}
	}
}

func TestHashToken_IsDeterministicAndIrreversible(t *testing.T) {
	const tok = "a-session-token"

	first, second := session.HashToken(tok), session.HashToken(tok)
	if !bytes.Equal(first, second) {
		t.Error("HashToken is not deterministic")
	}
	if len(first) != 32 {
		t.Errorf("HashToken returned %d bytes, want 32", len(first))
	}
	if bytes.Contains(first, []byte(tok)) {
		t.Error("the hash contains the token itself")
	}
	if bytes.Equal(first, session.HashToken(tok+"x")) {
		t.Error("two different tokens hash identically")
	}
}

func TestCSRF_RoundTripsAndRejectsMismatches(t *testing.T) {
	key, err := session.NewCSRFKey()
	if err != nil {
		t.Fatalf("NewCSRFKey: %v", err)
	}
	other, err := session.NewCSRFKey()
	if err != nil {
		t.Fatalf("NewCSRFKey: %v", err)
	}

	const tok = "session-token"
	good := session.CSRFToken(key, tok)

	if !session.VerifyCSRF(key, tok, good) {
		t.Error("a freshly derived CSRF token did not verify")
	}
	if session.VerifyCSRF(key, tok, good+"x") {
		t.Error("a tampered CSRF token verified")
	}
	if session.VerifyCSRF(key, "another-token", good) {
		t.Error("a CSRF token verified against a different session token")
	}
	// The per-session key is what closes naive double-submit: an attacker
	// who can set a cookie still cannot forge a token without the key.
	if session.VerifyCSRF(other, tok, good) {
		t.Error("a CSRF token verified under a different session's key")
	}
	if session.VerifyCSRF(key, tok, "") {
		t.Error("an empty CSRF token verified")
	}
}

func TestEntStore_CreateAndResolve(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()

	token, err := store.Create(ctx, testIdentity(), time.Hour, 8*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sess, err := store.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sess.Subject != "operator@example.com" {
		t.Errorf("Subject = %q, want operator@example.com", sess.Subject)
	}
	if sess.Role != auth.RoleOperator {
		t.Errorf("Role = %q, want operator", sess.Role)
	}
	if len(sess.CSRFKey) != 32 {
		t.Errorf("CSRFKey is %d bytes, want 32", len(sess.CSRFKey))
	}

	// The identity a session resolves to must be the same shape the
	// Bearer path produces, so both feed one evaluator.
	id := sess.Identity()
	if !id.HasScope(auth.ScopeJobRead) {
		t.Error("the resolved identity lost a scope")
	}
	if id.HasScope(auth.ScopeInventoryWrite) {
		t.Error("the resolved identity gained a scope it was never granted")
	}
}

// A database dump must not be a session-hijack kit. Only the hash is
// stored, so nothing in the row equals the cookie the browser holds.
func TestEntStore_StoresOnlyTheHash(t *testing.T) {
	store, client := newStore(t)
	ctx := t.Context()

	token, err := store.Create(ctx, testIdentity(), time.Hour, 8*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := client.Session.Query().All(ctx)
	if err != nil {
		t.Fatalf("querying sessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored %d sessions, want 1", len(rows))
	}
	if bytes.Equal(rows[0].TokenHash, []byte(token)) {
		t.Fatal("the raw token was stored")
	}
	if !bytes.Equal(rows[0].TokenHash, session.HashToken(token)) {
		t.Error("the stored value is not the token's hash")
	}
}

func TestEntStore_ResolveRejectsUnknownAndEmptyTokens(t *testing.T) {
	store, _ := newStore(t)

	for _, tok := range []string{"", "never-issued"} {
		if _, err := store.Resolve(t.Context(), tok); !errors.Is(err, session.ErrNotFound) {
			t.Errorf("Resolve(%q) = %v, want ErrNotFound", tok, err)
		}
	}
}

// Expiry is enforced on read, not left to the sweeper: correctness must
// not depend on how recently a periodic job last ran.
func TestEntStore_ResolveRefusesExpiredSessions(t *testing.T) {
	for _, tc := range []struct {
		name           string
		idle, absolute time.Duration
	}{
		{"idle expired", -time.Minute, time.Hour},
		{"absolute expired", time.Hour, -time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newStore(t)
			ctx := t.Context()

			token, err := store.Create(ctx, testIdentity(), tc.idle, tc.absolute)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := store.Resolve(ctx, token); !errors.Is(err, session.ErrNotFound) {
				t.Errorf("Resolve on an expired session = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestEntStore_TouchSlidesIdleButNeverAbsolute(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()

	token, err := store.Create(ctx, testIdentity(), time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, err := store.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := store.Touch(ctx, token, 2*time.Hour); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	after, err := store.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("Resolve after Touch: %v", err)
	}

	if !after.IdleExpiresAt.After(before.IdleExpiresAt) {
		t.Error("Touch did not slide the idle deadline forward")
	}
	// The absolute deadline is what stops a stolen cookie living forever
	// through continued use. If Touch moved it, there would effectively be
	// only one deadline.
	if !after.AbsoluteExpiresAt.Equal(before.AbsoluteExpiresAt) {
		t.Errorf("Touch moved the absolute deadline from %v to %v",
			before.AbsoluteExpiresAt, after.AbsoluteExpiresAt)
	}
}

func TestEntStore_TouchReportsAnUnknownSession(t *testing.T) {
	store, _ := newStore(t)
	if err := store.Touch(t.Context(), "never-issued", time.Hour); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Touch = %v, want ErrNotFound", err)
	}
}

// Revocation is the whole reason this store exists: a signed token cannot
// be withdrawn, and a session can.
func TestEntStore_DeleteRevokesImmediately(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()

	token, err := store.Create(ctx, testIdentity(), time.Hour, 8*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Resolve(ctx, token); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Resolve after Delete = %v, want ErrNotFound", err)
	}
	// Logging out twice is not an error: the caller asked for it to be
	// gone and it is gone.
	if err := store.Delete(ctx, token); err != nil {
		t.Errorf("second Delete = %v, want nil", err)
	}
}

func TestEntStore_DeleteExpiredRemovesOnlyPastTheAbsoluteDeadline(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()

	live, err := store.Create(ctx, testIdentity(), time.Hour, 8*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Create(ctx, testIdentity(), -time.Hour, -time.Minute); err != nil {
		t.Fatalf("Create expired: %v", err)
	}

	n, err := store.DeleteExpired(ctx, time.Now())
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteExpired removed %d sessions, want 1", n)
	}
	if _, err := store.Resolve(ctx, live); err != nil {
		t.Errorf("the live session was swept: %v", err)
	}
}

func TestEntStore_CreateRefusesANilIdentity(t *testing.T) {
	store, _ := newStore(t)
	if _, err := store.Create(t.Context(), nil, time.Hour, time.Hour); err == nil {
		t.Error("Create(nil) = nil error, want a refusal")
	}
}

func TestCookieCodec_SecureAttributes(t *testing.T) {
	codec := session.CookieCodec{}
	rec := httptest.NewRecorder()
	codec.Write(rec, "the-token")

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("wrote %d cookies, want 1", len(cookies))
	}
	c := cookies[0]

	// The __Host- prefix is browser-enforced: it requires Secure, Path=/
	// and no Domain, which is what stops a subdomain setting a cookie the
	// parent origin will send.
	if c.Name != session.SecureCookieName {
		t.Errorf("name = %q, want %q", c.Name, session.SecureCookieName)
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly, so every script on the page can read it")
	}
	if !c.Secure {
		t.Error("cookie is not Secure")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if c.Domain != "" {
		t.Errorf("Domain = %q, want empty for __Host-", c.Domain)
	}
	// No Max-Age or Expires: the server owns the deadlines, and a
	// client-held copy would disagree the moment a session was revoked.
	if c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Errorf("cookie carries its own expiry (MaxAge %d, Expires %v)", c.MaxAge, c.Expires)
	}
}

// TestCookieCodec_HasNoWeakerMode replaces a test that asserted the
// opposite.
//
// Until Phase 20 this codec carried an Insecure flag, and the test here
// asserted that setting it really did drop the __Host- prefix and the
// Secure attribute. The flag is gone, along with the arrangement that
// justified it, so what is worth pinning now is that Clear cannot forget
// what Write states: a logout that expired a cookie with different
// attributes would leave the original one in the browser on some clients.
func TestCookieCodec_HasNoWeakerMode(t *testing.T) {
	codec := session.CookieCodec{}

	written := httptest.NewRecorder()
	codec.Write(written, "the-token")
	cleared := httptest.NewRecorder()
	codec.Clear(cleared)

	set := written.Result().Cookies()[0]
	unset := cleared.Result().Cookies()[0]

	if set.Name != unset.Name {
		t.Errorf("Write set %q and Clear expired %q; a logout that names a different cookie deletes nothing", set.Name, unset.Name)
	}
	for _, c := range []*http.Cookie{set, unset} {
		if !c.Secure {
			t.Errorf("%s is not Secure, which the __Host- prefix requires the browser to refuse", c.Name)
		}
		if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
			t.Errorf("%s dropped one of the attributes that does not depend on TLS: %+v", c.Name, c)
		}
	}
}

func TestCookieCodec_ReadAndClear(t *testing.T) {
	codec := session.CookieCodec{}

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	if got := codec.Read(r); got != "" {
		t.Errorf("Read with no cookie = %q, want empty", got)
	}
	r.AddCookie(&http.Cookie{Name: session.SecureCookieName, Value: "abc"})
	if got := codec.Read(r); got != "abc" {
		t.Errorf("Read = %q, want abc", got)
	}

	rec := httptest.NewRecorder()
	codec.Clear(rec)
	c := rec.Result().Cookies()[0]
	if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("Clear wrote %+v, want an immediately-expiring empty cookie", c)
	}
}

func TestCookieSource_ResolvesAndFallsThrough(t *testing.T) {
	store, _ := newStore(t)
	codec := session.CookieCodec{}
	src := session.CookieSource{Store: store, Cookie: codec}

	token, err := store.Create(t.Context(), testIdentity(), time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("no cookie falls through", func(t *testing.T) {
		// (nil, nil) is "not my kind of request", which must let another
		// source authenticate rather than failing the request outright.
		id, err := src.Resolve(httptest.NewRequest(http.MethodGet, "/ui/", nil))
		if id != nil || err != nil {
			t.Errorf("Resolve with no cookie = (%v, %v), want (nil, nil)", id, err)
		}
	})

	t.Run("valid cookie resolves", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
		r.AddCookie(&http.Cookie{Name: codec.Name(), Value: token})

		id, err := src.Resolve(r)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if id == nil || id.Subject != "operator@example.com" {
			t.Fatalf("Resolve = %v, want the session's identity", id)
		}
	})

	t.Run("stale cookie is an error, not a fall-through", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
		r.AddCookie(&http.Cookie{Name: codec.Name(), Value: "never-issued"})

		if _, err := src.Resolve(r); !errors.Is(err, session.ErrNotFound) {
			t.Errorf("Resolve with a stale cookie = %v, want ErrNotFound", err)
		}
	})

	if src.Name() != "cookie" {
		t.Errorf("Name() = %q, want cookie", src.Name())
	}
}

// Resolving through the cookie source slides the idle deadline, so an
// actively used session does not expire under its user.
func TestCookieSource_ResolveTouchesTheSession(t *testing.T) {
	store, _ := newStore(t)
	codec := session.CookieCodec{}
	src := session.CookieSource{Store: store, Cookie: codec, IdleTimeout: 2 * time.Hour}

	token, err := store.Create(t.Context(), testIdentity(), time.Minute, 8*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, _ := store.Resolve(t.Context(), token)

	r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	r.AddCookie(&http.Cookie{Name: codec.Name(), Value: token})
	if _, err := src.Resolve(r); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	after, _ := store.Resolve(t.Context(), token)
	if !after.IdleExpiresAt.After(before.IdleExpiresAt) {
		t.Error("resolving through the cookie source did not slide the idle deadline")
	}
}

func TestSweepExpired_RunsOnlyAsLeader(t *testing.T) {
	store, _ := newStore(t)
	if _, err := store.Create(context.Background(), testIdentity(), -time.Hour, -time.Minute); err != nil {
		t.Fatalf("Create expired: %v", err)
	}

	t.Run("a follower sweeps nothing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		defer cancel()

		swept := make(chan int, 1)
		session.SweepExpired(ctx, store, 10*time.Millisecond, func() bool { return false }, func(n int) { swept <- n })

		select {
		case n := <-swept:
			t.Errorf("a follower swept %d sessions; exactly one replica should sweep", n)
		default:
		}
	})

	t.Run("the leader sweeps", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		swept := make(chan int, 1)
		go session.SweepExpired(ctx, store, 10*time.Millisecond, func() bool { return true }, func(n int) {
			select {
			case swept <- n:
			default:
			}
		})

		select {
		case n := <-swept:
			if n != 1 {
				t.Errorf("swept %d sessions, want 1", n)
			}
		case <-ctx.Done():
			t.Error("the leader never swept the expired session")
		}
	})
}

// TestDeleteForSubject_RevokesEverySessionForOneSubject is the operation a
// password change, an administrative reset and a deleted account all need.
//
// Delete takes a token, and a caller only ever holds the token for its own
// session, so without this there is no way to end the sessions somebody
// else is holding. That is the difference between changing a password and
// only appearing to.
func TestDeleteForSubject_RevokesEverySessionForOneSubject(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()

	target := &auth.Identity{Subject: "target@example.test", Role: auth.RoleOperator}
	bystander := &auth.Identity{Subject: "bystander@example.test", Role: auth.RoleViewer}

	first, err := store.Create(ctx, target, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := store.Create(ctx, target, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	other, err := store.Create(ctx, bystander, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	deleted, err := store.DeleteForSubject(ctx, "target@example.test", "")
	if err != nil {
		t.Fatalf("DeleteForSubject() error = %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	for name, token := range map[string]string{"first": first, "second": second} {
		if _, err := store.Resolve(ctx, token); err == nil {
			t.Errorf("the %s session for the target subject survived", name)
		}
	}
	if _, err := store.Resolve(ctx, other); err != nil {
		t.Errorf("another subject's session was revoked: %v", err)
	}
}

// TestDeleteForSubject_SparesTheNamedToken is what makes a self-service
// password change usable.
//
// Signing somebody out of the page they just used, as a reward for
// improving their own security, teaches them not to do it again.
func TestDeleteForSubject_SparesTheNamedToken(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()
	id := &auth.Identity{Subject: "operator@example.test", Role: auth.RoleOperator}

	keep, err := store.Create(ctx, id, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	drop, err := store.Create(ctx, id, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	deleted, err := store.DeleteForSubject(ctx, "operator@example.test", keep)
	if err != nil {
		t.Fatalf("DeleteForSubject() error = %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	if _, err := store.Resolve(ctx, keep); err != nil {
		t.Errorf("the spared session was revoked: %v", err)
	}
	if _, err := store.Resolve(ctx, drop); err == nil {
		t.Error("the other session survived")
	}
}

// TestDeleteForSubject_RefusesAnEmptySubject covers the guard.
//
// The schema requires a non-empty subject, so an empty argument is always a
// caller bug. Matching every row with an empty subject would delete nothing
// while reporting success, which is the worst of both answers.
func TestDeleteForSubject_RefusesAnEmptySubject(t *testing.T) {
	store, _ := newStore(t)

	if _, err := store.DeleteForSubject(t.Context(), "", ""); err == nil {
		t.Error("DeleteForSubject(\"\") = nil error")
	}
}

// TestDeleteForSubject_IsZeroForAnUnknownSubject proves it is not an error
// to revoke nothing: an account with no live sessions is the normal case
// for a reset, not a failure.
func TestDeleteForSubject_IsZeroForAnUnknownSubject(t *testing.T) {
	store, _ := newStore(t)

	deleted, err := store.DeleteForSubject(t.Context(), "nobody@example.test", "")
	if err != nil {
		t.Errorf("DeleteForSubject() error = %v, want nil", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}
