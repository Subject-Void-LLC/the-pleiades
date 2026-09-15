// This file covers the small decisions the page chrome makes before any
// view renders: who is signed in, what they may do, and what a skeleton
// view says about itself.
//
// Each is a handful of statements with an early return, and the early
// return is the interesting half. A permits that answered yes when it could
// not evaluate would render controls the caller cannot use; a subjectOf
// that panicked on a signed-out request would take down the sign-in page.
package web

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestSubjectOf_IsEmptyWhenSignedOut covers the value the sign-out control
// renders from.
func TestSubjectOf_IsEmptyWhenSignedOut(t *testing.T) {
	if got := subjectOf(nil); got != "" {
		t.Errorf("subjectOf(nil) = %q, want empty so no sign-out control renders", got)
	}
	if got := subjectOf(&auth.Identity{Subject: "operator@example.test"}); got != "operator@example.test" {
		t.Errorf("subjectOf() = %q", got)
	}
}

// TestPermits_RefusesWhenItCannotEvaluate is the fail-closed case.
//
// A missing admission chain or a signed-out caller must answer no. Answering
// yes would render an Edit button that the API then refuses, which teaches a
// reader that the UI's controls do not mean anything.
func TestPermits_RefusesWhenItCannotEvaluate(t *testing.T) {
	ctx := context.Background()
	id := &auth.Identity{Subject: "operator@example.test", Role: auth.RoleOperator}

	if (&Handler{}).permits(ctx, id, auth.ScopeInventoryRead) {
		t.Error("permits() = true with no admission chain wired")
	}
	withChain := &Handler{cfg: Config{Admission: allowAll{}}}
	if withChain.permits(ctx, nil, auth.ScopeInventoryRead) {
		t.Error("permits() = true for a signed-out caller")
	}
}

// TestDeclaredReason_SaysWhatIsActuallyMissing covers both halves of the
// sentence a skeleton view shows.
//
// The distinction is the point: "no backing API" and "handlers not
// implemented" are different states of the work, and a panel that said the
// same thing for both would be an apology rather than information.
func TestDeclaredReason_SaysWhatIsActuallyMissing(t *testing.T) {
	noAPI := declaredReason(view.Descriptor{Name: "labels", Title: "Labels"})
	if noAPI == "" {
		t.Fatal("a view with no endpoints explains nothing")
	}

	withAPI := declaredReason(view.Descriptor{
		Name:  "approvals",
		Title: "Approvals",
		Ops:   view.Ops{List: &apispec.ListProjects},
	})
	if withAPI == "" {
		t.Fatal("a view with endpoints explains nothing")
	}
	if noAPI == withAPI {
		t.Errorf("both states give the same reason %q, so the panel says nothing useful", noAPI)
	}
}
