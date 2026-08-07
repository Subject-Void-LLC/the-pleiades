// This file is Phase 12's own boundary: RequireScope is the one place a
// route's declared auth.Scope actually gets enforced against the
// authenticated caller, closing the gap FAILURE_PATTERNS.md #65 and #66
// named. AuthMiddleware (middleware.go) only answers "who is this
// caller"; nothing before this file ever asked "is this caller allowed to
// do this."
package api

import (
	"context"
	"net/http"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// Admitter is the narrow slice of auth.Admission RequireScope needs: one
// method, evaluated against an already-authenticated identity and the
// scope a route declares. Depending on this instead of *auth.Admission
// directly is the same Interface Segregation shape TokenValidator already
// uses above, and it is what lets a test supply a two-line double instead
// of a real Chain and Recorder.
type Admitter interface {
	Evaluate(ctx context.Context, id *auth.Identity, req auth.AdmissionRequest) error
}

// RequireScope enforces that the identity AuthMiddleware placed in context
// is admitted for scope, per admitter's chain. It is mounted once per
// route, keyed on that route's own declared auth.Scope (Route.Scope in
// router.go), rather than once for the whole /api/v1 subtree: two routes
// can require two different scopes, and a shared middleware instance
// cannot know at registration time which one a given request is headed
// for.
//
// A missing identity is answered with 401, not a panic and not a silent
// pass-through. Reaching this middleware with no identity in context means
// AuthMiddleware did not run in front of it, which is either a
// construction bug NewRouter's own validation is supposed to make
// impossible, or a direct handler call in a test that skipped the chain on
// purpose; either way, the fail-closed answer is the same one
// AdmissionChain itself already gives an unauthenticated caller
// (auth.scopeRule's own "id == nil" branch), not an assumption that an
// absent identity means "allow."
func RequireScope(admitter Admitter, scope auth.Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := IdentityFromContext(r.Context())
			if !ok {
				RespondError(w, r, http.StatusUnauthorized, "unauthorized")
				return
			}

			req := auth.AdmissionRequest{RequiredScope: scope}
			if err := admitter.Evaluate(r.Context(), id, req); err != nil {
				// The chain's own error (auth.AdmissionChain.Evaluate)
				// already names the denial reason, but that string is not
				// returned to the caller: PATTERNS.md's /readyz precedent
				// applies equally here, an authenticated caller still gets
				// no detail about *why* it was denied beyond the status
				// code, since the reason can itself leak which scopes or
				// targets exist. auth.Recorder (wired into admitter, not
				// this middleware) is where the real reason goes, for an
				// operator to read.
				RespondError(w, r, http.StatusForbidden, "forbidden")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
