package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// fakeAdmitter is a two-line Admitter double: it records the last request
// it was asked to evaluate and returns Err. This is the Interface
// Segregation payoff RequireScope's own doc comment names: a test needs
// none of auth.Admission's Chain/Recorder machinery to exercise the
// middleware.
type fakeAdmitter struct {
	Err     error
	LastReq auth.AdmissionRequest
	Calls   int
}

func (f *fakeAdmitter) Evaluate(_ context.Context, _ *auth.Identity, req auth.AdmissionRequest) error {
	f.Calls++
	f.LastReq = req
	return f.Err
}

func reachedHandler(reached *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	}
}

// TestRequireScope_NoIdentityIs401 proves reaching RequireScope with no
// identity in context (AuthMiddleware never ran, or a test called the
// handler chain directly) fails closed with 401, the same answer
// auth.scopeRule itself gives an unauthenticated identity, rather than
// treating an absent identity as an implicit allow.
func TestRequireScope_NoIdentityIs401(t *testing.T) {
	admitter := &fakeAdmitter{}
	var reached bool
	handler := api.RequireScope(admitter, auth.ScopeRunbookExecute)(reachedHandler(&reached))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/jobs/dispatch", nil))

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	if reached {
		t.Error("handler ran despite no identity in context")
	}
	if admitter.Calls != 0 {
		t.Errorf("admitter was called %d times, want 0: there is nothing to admit without an identity", admitter.Calls)
	}
}

// TestRequireScope_DeniedIs403 proves a denial from the admitter becomes
// 403, not 401: the caller is known (authentication succeeded), it is
// simply not allowed to do this, and PATTERNS.md's Confused Deputy
// Defense entry (Section 21.3) names 403 by name as the answer to an
// out-of-scope request.
func TestRequireScope_DeniedIs403(t *testing.T) {
	admitter := &fakeAdmitter{Err: errors.New("access denied: admission rule denied access")}
	var reached bool
	handler := api.RequireScope(admitter, auth.ScopeRunbookExecute)(reachedHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/jobs/dispatch", nil)
	req = req.WithContext(contextWithIdentity(req, &auth.Identity{Subject: "u1", Role: auth.RoleViewer}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusForbidden)
	}
	if reached {
		t.Error("handler ran despite a denied admission decision")
	}
	if admitter.LastReq.RequiredScope != auth.ScopeRunbookExecute {
		t.Errorf("admitter was asked about scope %q, want %q", admitter.LastReq.RequiredScope, auth.ScopeRunbookExecute)
	}
}

// TestRequireScope_AllowedReachesHandler is the positive control: without
// it, TestRequireScope_NoIdentityIs401 and TestRequireScope_DeniedIs403
// could both be passing because reachedHandler can never run at all.
func TestRequireScope_AllowedReachesHandler(t *testing.T) {
	admitter := &fakeAdmitter{}
	var reached bool
	handler := api.RequireScope(admitter, auth.ScopeRunbookExecute)(reachedHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/jobs/dispatch", nil)
	req = req.WithContext(contextWithIdentity(req, &auth.Identity{Subject: "u1", Role: auth.RoleAdmin}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", rr.Code, http.StatusOK)
	}
	if !reached {
		t.Error("handler did not run despite an allowed admission decision")
	}
}
