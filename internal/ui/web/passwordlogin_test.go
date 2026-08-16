// Tests for password sign-in through the real handler.
//
// The credential store and the identity deriver are doubles here, and that
// is the correct boundary rather than a shortcut: internal/localauth already
// proves Argon2id, lockout and timing against a real database, and
// internal/auth already proves the derivation against real RoleBindings.
// What is unproven until this file is the HANDLER's own contract, which is
// that it picks the right credential path, mints a session from a derived
// identity, and refuses everything else identically. A real store here would
// re-prove somebody else's work and add thirty milliseconds of Argon2id to
// every case.
package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/go-chi/chi/v5"
)

// errRefused stands in for localauth.ErrInvalidCredentials, which this
// package deliberately cannot import.
var errRefused = errors.New("invalid credentials")

// fakePasswords answers for exactly one pair and refuses everything else,
// the way the real store does.
type fakePasswords struct {
	email    string
	password string
	subject  string
	calls    int
}

func (f *fakePasswords) Authenticate(_ context.Context, email, password string) (string, error) {
	f.calls++
	if email == f.email && password == f.password {
		return f.subject, nil
	}
	return "", errRefused
}

// fakeIdentities returns a fixed identity, or an error, for any subject.
type fakeIdentities struct {
	identity *auth.Identity
	err      error
}

func (f fakeIdentities) Build(_ context.Context, subject string) (*auth.Identity, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := *f.identity
	out.Subject = subject
	return &out, nil
}

func newPasswordProbe(t *testing.T, passwords PasswordAuthenticator, identities IdentityDeriver) *loginProbe {
	t.Helper()
	registerTestView()

	issuer := authtest.New(t, "ui-login-test", "ui-login-audience")
	store := newMemStore()
	cookie := session.CookieCodec{}

	h := New(Config{
		Prefix:     "/ui",
		Sessions:   store,
		Cookie:     cookie,
		Tokens:     issuer.Evaluator(),
		Passwords:  passwords,
		Identities: identities,
		HATEOAS:    permitEverything{},
		Admission:  allowAll{},
	})
	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())

	return &loginProbe{probe: &probe{mux: root, store: store, cookie: cookie}, issuer: issuer}
}

func operatorIdentity() *auth.Identity {
	return &auth.Identity{
		Role:   auth.RoleOperator,
		Scopes: auth.ScopesForRole(auth.RoleOperator),
	}
}

// TestPasswordLogin_MintsASessionFromADerivedIdentity is the feature the
// whole phase exists for: a person on a clean machine types an email and a
// password and reaches an authenticated page.
func TestPasswordLogin_MintsASessionFromADerivedIdentity(t *testing.T) {
	passwords := &fakePasswords{
		email:    "operator@example.test",
		password: "a-real-test-password",
		subject:  "operator@example.test",
	}
	p := newPasswordProbe(t, passwords, fakeIdentities{identity: operatorIdentity()})

	rec := p.postForm("/ui/login", "email=operator@example.test&password=a-real-test-password")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	c := p.sessionCookie(rec)
	if c == nil {
		t.Fatal("no session cookie was minted")
	}
	// The session must carry the DERIVED authority, not a default and not
	// nothing. This is what separates a login that works from one that
	// authenticates and then can reach no page.
	sess, err := p.store.Resolve(t.Context(), c.Value)
	if err != nil {
		t.Fatalf("resolving the minted session: %v", err)
	}
	if sess.Role != auth.RoleOperator {
		t.Errorf("session role = %q, want operator", sess.Role)
	}
	if !sess.Identity().HasScope(auth.ScopeRunbookExecute) {
		t.Error("the derived session cannot execute a runbook, so the derivation did not reach the session")
	}

	// And it must actually authenticate a subsequent request.
	req := httptest.NewRequest(http.MethodGet, "/ui/"+testView, nil)
	req.AddCookie(c)
	if got := p.serve(req); got.Code != http.StatusOK {
		t.Errorf("the minted session did not authenticate a read: status = %d", got.Code)
	}
}

// TestPasswordLogin_NeverEchoesTheSubmittedPassword is worth its own test
// because a re-rendered form is the natural place to helpfully repopulate
// fields, and repopulating this one puts a password in browser history, in a
// proxy log and in a screenshot.
func TestPasswordLogin_NeverEchoesTheSubmittedPassword(t *testing.T) {
	const password = "a-very-distinctive-passphrase"
	passwords := &fakePasswords{email: "operator@example.test", password: "something-else", subject: "operator@example.test"}
	p := newPasswordProbe(t, passwords, fakeIdentities{identity: operatorIdentity()})

	rec := p.postForm("/ui/login", "email=operator@example.test&password="+password)
	if strings.Contains(rec.Body.String(), password) {
		t.Error("the re-rendered login page echoed the submitted password")
	}
}

// TestPasswordLogin_EveryFailureLooksTheSame extends the token path's own
// property across the credential boundary. A response that distinguished a
// wrong password from an unknown address would be the account-existence
// oracle the store works to avoid, reintroduced one layer up.
func TestPasswordLogin_EveryFailureLooksTheSame(t *testing.T) {
	passwords := &fakePasswords{
		email:    "operator@example.test",
		password: "a-real-test-password",
		subject:  "operator@example.test",
	}
	p := newPasswordProbe(t, passwords, fakeIdentities{identity: operatorIdentity()})

	bodies := []string{}
	for _, tc := range []struct{ name, form string }{
		{"wrong password", "email=operator@example.test&password=wrong"},
		{"unknown address", "email=nobody@example.test&password=a-real-test-password"},
		{"empty password", "email=operator@example.test&password="},
		{"empty email", "email=&password=a-real-test-password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := p.postForm("/ui/login", tc.form)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if p.sessionCookie(rec) != nil {
				t.Error("a failed password login still minted a session")
			}
			bodies = append(bodies, withoutCSRFToken(rec.Body.String()))
		})
	}

	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Error("two password failures rendered different pages, which distinguishes them to an attacker")
			break
		}
	}
}

// TestPasswordLogin_ReachesTheStoreEvenForAnEmptyEmail proves the handler
// does not short-circuit before the store.
//
// The store equalizes timing between a known and an unknown account by
// running a decoy derivation, and it can only do that if it is actually
// called. A handler that rejected an empty or malformed address itself would
// silently reintroduce the timing oracle the store went to trouble to close.
func TestPasswordLogin_ReachesTheStoreEvenForAnEmptyEmail(t *testing.T) {
	passwords := &fakePasswords{email: "operator@example.test", password: "pw", subject: "operator@example.test"}
	p := newPasswordProbe(t, passwords, fakeIdentities{identity: operatorIdentity()})

	p.postForm("/ui/login", "email=&password=some-password")
	if passwords.calls == 0 {
		t.Error("the handler refused an empty address without consulting the store, " +
			"which skips the decoy derivation and makes the unknown-account case measurably faster")
	}
}

// TestPasswordLogin_RefusedWhenNotConfigured covers a deployment that
// federates and holds no local credentials.
func TestPasswordLogin_RefusedWhenNotConfigured(t *testing.T) {
	// No Passwords, no Identities: the token path only.
	p := newLoginProbe(t)

	rec := p.postForm("/ui/login", "email=operator@example.test&password=anything")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if p.sessionCookie(rec) != nil {
		t.Error("password login succeeded with no credential store configured")
	}
	// And the form must not offer fields it cannot honor.
	page := p.serve(httptest.NewRequest(http.MethodGet, "/ui/login", nil)).Body.String()
	if strings.Contains(page, `name="password"`) {
		t.Error("the sign-in page offers a password field on a deployment with no password store")
	}
}

// TestLoginPage_OffersPasswordFieldsWhenConfigured is the other half.
func TestLoginPage_OffersPasswordFieldsWhenConfigured(t *testing.T) {
	p := newPasswordProbe(t, &fakePasswords{}, fakeIdentities{identity: operatorIdentity()})

	page := p.serve(httptest.NewRequest(http.MethodGet, "/ui/login", nil)).Body.String()
	for _, want := range []string{`name="email"`, `name="password"`, `autocomplete="current-password"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the sign-in page is missing %s", want)
		}
	}
	// The token field survives as the break-glass route.
	if !strings.Contains(page, `name="token"`) {
		t.Error("the token field was removed; it is the documented break-glass route for a federated deployment")
	}
}

// TestPasswordLogin_RefusesWhenTheDerivationFails covers the case where the
// person IS who they say they are and the deployment cannot work out what
// they may do.
//
// No session, because a session with no derived authority is worse than no
// session: it looks signed in and can reach nothing, which reads as data
// loss rather than as a server fault.
func TestPasswordLogin_RefusesWhenTheDerivationFails(t *testing.T) {
	passwords := &fakePasswords{
		email:    "operator@example.test",
		password: "a-real-test-password",
		subject:  "operator@example.test",
	}
	p := newPasswordProbe(t, passwords, fakeIdentities{err: errors.New("bindings unreadable")})

	rec := p.postForm("/ui/login", "email=operator@example.test&password=a-real-test-password")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if p.sessionCookie(rec) != nil {
		t.Error("a session was minted despite the identity derivation failing")
	}
}

// TestPasswordLogin_PrefersThePasswordFieldsOverAToken proves the handler
// picks one credential deterministically rather than by whichever field the
// browser happened to autofill.
func TestPasswordLogin_PrefersThePasswordFieldsOverAToken(t *testing.T) {
	passwords := &fakePasswords{
		email:    "operator@example.test",
		password: "a-real-test-password",
		subject:  "operator@example.test",
	}
	p := newPasswordProbe(t, passwords, fakeIdentities{identity: operatorIdentity()})
	token := p.issuer.Token(t, &auth.Identity{Subject: "someone-else@example.test", Role: auth.RoleAdmin})

	// A valid token AND a wrong password. The password path must win and the
	// request must fail, rather than silently falling through to the token
	// and signing the caller in as a different, more privileged subject.
	rec := p.postForm("/ui/login", "email=operator@example.test&password=wrong&token="+token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: a wrong password fell through to the token field", rec.Code)
	}
	if p.sessionCookie(rec) != nil {
		t.Error("a session was minted from the token after the password was refused")
	}
}
