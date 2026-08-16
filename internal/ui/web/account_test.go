// Tests for the self-service account page.
//
// The properties worth proving here are all about REACH rather than about
// hashing: that the route cannot be aimed at another account, that it is
// not shadowed by the resource routes it sits beside, that a change revokes
// the caller's other sessions and keeps this one, and that a deployment
// without local credentials does not render a control that can only fail.
package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/go-chi/chi/v5"
)

// fakeChanger records what it was asked to change and answers as told.
type fakeChanger struct {
	subject string
	current string
	err     error

	gotSubject string
	calls      int
}

func (f *fakeChanger) ChangePassword(_ context.Context, subject, oldPassword, newPassword string) error {
	f.calls++
	f.gotSubject = subject
	if f.err != nil {
		return f.err
	}
	if subject != f.subject || oldPassword != f.current {
		return errors.New("invalid credentials")
	}
	return nil
}

// accountProbe is a signed-in probe: unlike loginProbe, it starts with a
// live session, because every route here is behind requireSession.
type accountProbe struct {
	*probe
	changer *fakeChanger
	subject string
	token   string
}

func newAccountProbe(t *testing.T, changer PasswordChanger) *accountProbe {
	t.Helper()
	registerTestView()

	const subject = "operator@example.test"
	store := newMemStore()
	cookie := session.CookieCodec{}

	token, err := store.Create(t.Context(),
		&auth.Identity{Subject: subject, Role: auth.RoleOperator, Scopes: auth.ScopesForRole(auth.RoleOperator)},
		time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("creating the probe session: %v", err)
	}

	h := New(Config{
		Prefix:          "/ui",
		Sessions:        store,
		Cookie:          cookie,
		PasswordChanges: changer,
		HATEOAS:         permitEverything{},
		Admission:       allowAll{},
	})
	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())

	p := &accountProbe{
		probe:   &probe{mux: root, store: store, cookie: cookie, token: token},
		subject: subject,
		token:   token,
	}
	if fc, ok := changer.(*fakeChanger); ok {
		p.changer = fc
	}
	return p
}

// authed builds a request carrying the probe's session and CSRF token.
func (p *accountProbe) authed(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.AddCookie(&http.Cookie{Name: p.cookie.Name(), Value: p.token})

	sess, err := p.store.Resolve(context.Background(), p.token)
	if err == nil {
		r.Header.Set(session.CSRFHeader, session.CSRFToken(sess.CSRFKey, p.token))
	}
	return r
}

// TestAccountPage_IsNotShadowedByTheResourceRoutes is the routing check.
//
// /account sits in the same group as /{resource}, and /account/password
// beside /{resource}/{id}. chi resolves static segments before parameters,
// so these win, but that is a property of the router rather than something
// this package controls, and a resource literally named "account" would be
// the collision. Asserted rather than assumed, because the failure is
// silent: the account page would render a resource listing.
func TestAccountPage_IsNotShadowedByTheResourceRoutes(t *testing.T) {
	p := newAccountProbe(t, &fakeChanger{subject: "operator@example.test", current: "old-password"})

	rec := p.serve(p.authed(http.MethodGet, "/ui/account", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/account = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Change password") {
		t.Error("GET /ui/account did not render the account page; it is shadowed by the resource routes")
	}
	if !strings.Contains(body, `name="current_password"`) {
		t.Error("the account page has no current-password field")
	}
}

// TestChangePassword_UsesTheSessionSubjectNotTheForm is the authorization
// property, and it is the reason the route carries no {id}.
//
// A form field naming another account must be ignored entirely, because
// there is no field for it: the subject comes from the session. This test
// submits one anyway, the way an attacker would.
func TestChangePassword_UsesTheSessionSubjectNotTheForm(t *testing.T) {
	changer := &fakeChanger{subject: "operator@example.test", current: "old-password"}
	p := newAccountProbe(t, changer)

	rec := p.serve(p.authed(http.MethodPost, "/ui/account/password",
		"current_password=old-password&new_password=a-new-password&confirm_password=a-new-password"+
			"&email=victim@example.test&subject=victim@example.test"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if changer.gotSubject != "operator@example.test" {
		t.Errorf("the change was applied to %q, want the session's own subject; a form field steered it",
			changer.gotSubject)
	}
}

// TestChangePassword_RevokesOtherSessionsAndKeepsThisOne is the point of
// changing a password: the reason to do it is usually that somebody else
// may have had it.
func TestChangePassword_RevokesOtherSessionsAndKeepsThisOne(t *testing.T) {
	changer := &fakeChanger{subject: "operator@example.test", current: "old-password"}
	p := newAccountProbe(t, changer)

	// A second session for the same subject, as if signed in elsewhere.
	other, err := p.store.Create(t.Context(),
		&auth.Identity{Subject: p.subject, Role: auth.RoleOperator}, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("creating the second session: %v", err)
	}
	// And one belonging to somebody else, which must survive.
	stranger, err := p.store.Create(t.Context(),
		&auth.Identity{Subject: "someone-else@example.test", Role: auth.RoleViewer}, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("creating the stranger's session: %v", err)
	}

	rec := p.serve(p.authed(http.MethodPost, "/ui/account/password",
		"current_password=old-password&new_password=a-new-password&confirm_password=a-new-password"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	if _, err := p.store.Resolve(t.Context(), other); err == nil {
		t.Error("the caller's other session survived a password change")
	}
	if _, err := p.store.Resolve(t.Context(), p.token); err != nil {
		t.Error("the caller's own session was revoked, so changing a password signs you out of the page you did it on")
	}
	if _, err := p.store.Resolve(t.Context(), stranger); err != nil {
		t.Error("another subject's session was revoked by this caller's password change")
	}
}

// TestChangePassword_RefusesMismatchedConfirmationBeforeReachingTheStore
// covers the one failure the store cannot see.
func TestChangePassword_RefusesMismatchedConfirmationBeforeReachingTheStore(t *testing.T) {
	changer := &fakeChanger{subject: "operator@example.test", current: "old-password"}
	p := newAccountProbe(t, changer)

	rec := p.serve(p.authed(http.MethodPost, "/ui/account/password",
		"current_password=old-password&new_password=one-password&confirm_password=another-password"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if changer.calls != 0 {
		t.Error("a mismatched confirmation still reached the credential store")
	}
	if !strings.Contains(rec.Body.String(), "did not match") {
		t.Error("the page does not say the two new passwords differed")
	}
}

// TestChangePassword_ExplainsAWeakPasswordAndNotAWrongOne is the
// information-disclosure line.
//
// A refused NEW password is worth explaining: the person can act on it, and
// "rejected" with no reason leaves them guessing at a policy nobody wrote
// down. A wrong CURRENT password must say nothing beyond that it failed.
func TestChangePassword_ExplainsAWeakPasswordAndNotAWrongOne(t *testing.T) {
	t.Run("weak new password is explained", func(t *testing.T) {
		p := newAccountProbe(t, &fakeChanger{
			err: errors.Join(ErrWeakPassword, errors.New("needs at least 12 characters")),
		})
		rec := p.serve(p.authed(http.MethodPost, "/ui/account/password",
			"current_password=old-password&new_password=short&confirm_password=short"))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "at least 12 characters") {
			t.Error("a refused new password did not say why, leaving the person guessing at the policy")
		}
	})

	t.Run("wrong current password explains nothing", func(t *testing.T) {
		p := newAccountProbe(t, &fakeChanger{err: errors.New("invalid credentials")})
		rec := p.serve(p.authed(http.MethodPost, "/ui/account/password",
			"current_password=wrong&new_password=a-new-password&confirm_password=a-new-password"))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "invalid credentials") {
			t.Error("the page leaked the store's own error text")
		}
	})
}

// TestChangePassword_RequiresASession proves the route is inside the
// authenticated group rather than beside the login page.
func TestChangePassword_RequiresASession(t *testing.T) {
	p := newAccountProbe(t, &fakeChanger{subject: "operator@example.test", current: "old-password"})

	// No cookie, no CSRF header.
	r := httptest.NewRequest(http.MethodPost, "/ui/account/password",
		strings.NewReader("current_password=old-password&new_password=a-new-password&confirm_password=a-new-password"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := p.serve(r)
	// A 303 here is requireSession redirecting to the sign-in page, which
	// is the correct answer and looks exactly like a successful change on
	// the status line alone. The destination is what distinguishes them,
	// so that is what this asserts.
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/login") {
		t.Errorf("Location = %q, want the sign-in page; an unauthenticated caller was not redirected", loc)
	}
	// The assertion that actually matters either way: nothing reached the
	// credential store.
	if p.changer.calls != 0 {
		t.Error("an unauthenticated request reached the credential store")
	}
}

// TestChangePassword_RequiresTheCSRFToken proves the route is inside the
// CSRF group too, which is a separate membership from requireSession.
func TestChangePassword_RequiresTheCSRFToken(t *testing.T) {
	p := newAccountProbe(t, &fakeChanger{subject: "operator@example.test", current: "old-password"})

	r := httptest.NewRequest(http.MethodPost, "/ui/account/password",
		strings.NewReader("current_password=old-password&new_password=a-new-password&confirm_password=a-new-password"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: p.cookie.Name(), Value: p.token})
	// Session but no CSRF token.

	rec := p.serve(r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if p.changer.calls != 0 {
		t.Error("a request with no CSRF token reached the credential store")
	}
}

// TestAccountPage_AbsentWithoutALocalCredentialStore covers a deployment
// that federates: a password control it cannot honor is worse than none.
func TestAccountPage_AbsentWithoutALocalCredentialStore(t *testing.T) {
	registerTestView()
	store := newMemStore()
	cookie := session.CookieCodec{}
	token, err := store.Create(context.Background(),
		&auth.Identity{Subject: "operator@example.test", Role: auth.RoleOperator}, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("creating the probe session: %v", err)
	}

	// No PasswordChanges configured.
	h := New(Config{
		Prefix:    "/ui",
		Sessions:  store,
		Cookie:    cookie,
		HATEOAS:   permitEverything{},
		Admission: allowAll{},
	})
	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())
	p := &accountProbe{probe: &probe{mux: root, store: store, cookie: cookie, token: token}, token: token}

	rec := p.serve(p.authed(http.MethodPost, "/ui/account/password",
		"current_password=x&new_password=a-new-password&confirm_password=a-new-password"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no credential store is wired", rec.Code)
	}
}
