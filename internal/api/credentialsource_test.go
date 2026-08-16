package api_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
)

// These cover the credential-source seam directly.
//
// It exists because a browser cannot present a Bearer token -- an
// EventSource cannot set headers at all -- so the SSE job log stream was
// unreachable from any browser client until a second credential kind
// existed. That makes the ordering and the fall-through rules below
// security behaviour rather than plumbing, and they are the kind of rule
// that reads as obviously correct right up until someone reorders two lines.

// stubSource is a CredentialSource whose behaviour each test dictates.
type stubSource struct {
	name     string
	identity *auth.Identity
	err      error
	called   *bool
}

func (s stubSource) Name() string { return s.name }

func (s stubSource) Resolve(*http.Request) (*auth.Identity, error) {
	if s.called != nil {
		*s.called = true
	}
	return s.identity, s.err
}

// echoIdentity reports which identity, if any, reached the handler.
func echoIdentity(w http.ResponseWriter, r *http.Request) {
	id, ok := api.IdentityFromContext(r.Context())
	if !ok || id == nil {
		w.WriteHeader(http.StatusTeapot)
		return
	}
	_, _ = w.Write([]byte(id.Subject))
}

func serve(mw func(http.Handler) http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	mw(http.HandlerFunc(echoIdentity)).ServeHTTP(w, r)
	return w
}

// TestIdentityMiddleware_FirstSourceWithACredentialWins asserts the
// documented order: an explicit header is an unambiguous statement of
// intent, a cookie is ambient, so cookie-first would let a stale session
// silently override a token a caller deliberately supplied.
func TestIdentityMiddleware_FirstSourceWithACredentialWins(t *testing.T) {
	var secondCalled bool

	mw := api.IdentityMiddleware(nil,
		stubSource{name: "first", identity: &auth.Identity{Subject: "from-first"}},
		stubSource{name: "second", identity: &auth.Identity{Subject: "from-second"}, called: &secondCalled},
	)

	w := serve(mw, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := w.Body.String(); got != "from-first" {
		t.Errorf("identity = %q, want the first source's", got)
	}
	if secondCalled {
		t.Error("the second source was consulted even though the first supplied an identity")
	}
}

// TestIdentityMiddleware_NotMyKindFallsThrough covers the distinction the
// interface is built around: (nil, nil) means "this request carries no
// credential of my kind" and must fall through, which is different from
// "a credential of my kind, and it is bad".
func TestIdentityMiddleware_NotMyKindFallsThrough(t *testing.T) {
	mw := api.IdentityMiddleware(nil,
		stubSource{name: "absent"}, // (nil, nil)
		stubSource{name: "present", identity: &auth.Identity{Subject: "from-second"}},
	)

	w := serve(mw, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := w.Body.String(); got != "from-second" {
		t.Errorf("identity = %q, want the second source's", got)
	}
}

// TestIdentityMiddleware_ABadCredentialIsRejectedNotSkipped is the
// other half, and the one that would be a real hole if it were wrong: a
// source that recognised the credential and rejected it must end the
// request, never fall through to a source that would let it in.
func TestIdentityMiddleware_ABadCredentialIsRejectedNotSkipped(t *testing.T) {
	var laterCalled bool

	mw := api.IdentityMiddleware(nil,
		stubSource{name: "bad", err: errors.New("expired token")},
		stubSource{name: "permissive", identity: &auth.Identity{Subject: "should-not-be-reached"}, called: &laterCalled},
	)

	w := serve(mw, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("a rejected credential = %d, want 401", w.Code)
	}
	if laterCalled {
		t.Error("a later source was consulted after one rejected the request, so a bad " +
			"credential could be laundered into a good one")
	}
}

// TestIdentityMiddleware_NoCredentialAtAllIs401.
func TestIdentityMiddleware_NoCredentialAtAllIs401(t *testing.T) {
	mw := api.IdentityMiddleware(nil, stubSource{name: "absent"})

	if w := serve(mw, httptest.NewRequest(http.MethodGet, "/", nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("an anonymous request = %d, want 401", w.Code)
	}
}

// TestIdentityMiddleware_CustomUnauthorizedRenderer is what lets one
// condition owe two different answers: the UI redirects a browser to a login
// page where the JSON API returns 401.
func TestIdentityMiddleware_CustomUnauthorizedRenderer(t *testing.T) {
	mw := api.IdentityMiddleware(
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
		},
		stubSource{name: "absent"},
	)

	w := serve(mw, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusSeeOther {
		t.Errorf("with a custom renderer, an anonymous request = %d, want 303", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/ui/login" {
		t.Errorf("Location = %q, want the login page", got)
	}
}

// TestBearerSource covers the credential kind every API and CLI client uses,
// including the shapes that are not a bearer token at all and must fall
// through rather than be rejected.
func TestBearerSource(t *testing.T) {
	issuer := authtest.New(t, "pleiades-test", "pleiades")
	source := api.BearerSource{Validator: issuer.Evaluator()}

	if source.Name() != "bearer" {
		t.Errorf("Name() = %q, want bearer", source.Name())
	}

	t.Run("a valid token resolves", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", issuer.BearerToken(t, &auth.Identity{Subject: "alice", Role: auth.RoleAdmin}))

		id, err := source.Resolve(r)
		if err != nil {
			t.Fatalf("Resolve() = %v, want nil", err)
		}
		if id == nil || id.Subject != "alice" {
			t.Fatalf("Resolve() identity = %+v, want alice", id)
		}
	})

	t.Run("a forged token is rejected, not skipped", func(t *testing.T) {
		forged := authtest.NewWithSecret(t, "a-completely-different-signing-secret", "pleiades-test", "pleiades")

		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", forged.BearerToken(t, &auth.Identity{Subject: "mallory"}))

		id, err := source.Resolve(r)
		if err == nil {
			t.Fatal("Resolve() accepted a token signed with the wrong key")
		}
		if id != nil {
			t.Errorf("Resolve() returned an identity alongside its error: %+v", id)
		}
	})

	for _, tc := range []struct{ name, header string }{
		{"no header", ""},
		{"the word Bearer alone", "Bearer"},
		{"another scheme", "Basic dXNlcjpwYXNz"},
		{"a bare token with no scheme", "eyJhbGciOiJIUzI1NiJ9.e30.x"},
	} {
		t.Run(tc.name+" falls through", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}

			id, err := source.Resolve(r)
			if id != nil || err != nil {
				t.Errorf("Resolve() = (%+v, %v), want (nil, nil): this is not a bearer "+
					"request and must fall through to the next source", id, err)
			}
		})
	}
}

// TestAuthMiddleware_IsStillTheBearerOnlyWrapper. It is kept verbatim so
// cmd/demo and every existing test are untouched by the generalization, and
// this is what proves that claim rather than assuming it.
func TestAuthMiddleware_IsStillTheBearerOnlyWrapper(t *testing.T) {
	issuer := authtest.New(t, "pleiades-test", "pleiades")
	mw := api.AuthMiddleware(issuer.Evaluator())

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", issuer.BearerToken(t, &auth.Identity{Subject: "bob"}))

	if got := serve(mw, r).Body.String(); got != "bob" {
		t.Errorf("identity = %q, want bob", got)
	}

	// A cookie must not authenticate here: this wrapper carries exactly one
	// source, and a request with no Authorization header is anonymous to it.
	cookied := httptest.NewRequest(http.MethodGet, "/", nil)
	// The real session cookie name, so this stays a statement about the
	// wrapper rather than about a string. It was an unprefixed literal
	// until Phase 20 deleted the insecure cookie mode that produced one.
	cookied.AddCookie(&http.Cookie{Name: session.SecureCookieName, Value: "irrelevant"})

	if w := serve(mw, cookied); w.Code != http.StatusUnauthorized {
		t.Errorf("a cookie reached the Bearer-only wrapper: %d, want 401", w.Code)
	}
}
