// This file is the encoder seam that replaced Phase 13's response-rewriting
// middleware.
//
// The middleware buffered every handler's entire body, decoded it into a
// map[string]interface{}, added a key, and re-encoded it. That shape cost
// four distinct defects, all recorded: it dropped http.Flusher and would
// have killed the SSE log stream the moment it was mounted
// (FAILURE_PATTERNS.md #70), it corrupted integers past 2^53 and skipped
// collections entirely (#71), it reflected the caller's own path back as a
// hypermedia href (#72), and it discarded the authorization error (#73).
//
// A seam the handler calls has none of those. It never wraps the
// ResponseWriter, so a streaming handler keeps every optional interface it
// needs. It marshals the handler's own typed value exactly once and never
// decodes it, so nothing can be lost in a round trip. And it computes links
// before the first byte is written, so a failure can still choose an honest
// status code.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// Link is one hypermedia affordance in a response's _links array: the
// relation a client keys on, the URL it acts against, and the method it
// uses.
//
// It lives here rather than in pkg/wire because pkg/wire does not exist
// yet: PLAN.md Section 25 assigns the wire-contract package to Phase 15,
// and Phase 14 owns moving DispatchPayload into it. Section 25's rule is
// that a contract has exactly one implementation, which this satisfies:
// there is one definition of Link in this repository. The forward
// constraint worth recording is that Link and auth.LinkRel have to move
// together, because pkg/ may not import internal/.
type Link struct {
	Rel    auth.LinkRel `json:"rel"`
	Href   string       `json:"href"`
	Method string       `json:"method"`
}

// Linkable is satisfied by any response payload that can carry its own
// affordances. Respond fills the field before encoding.
//
// Embedding LinkSet is how a DTO satisfies this; see its doc comment for
// why _links is a typed field rather than a map entry.
type Linkable interface {
	SetLinks(links []Link)
}

// LinkFilter is optionally satisfied by a response payload that wants to
// suppress affordances its own state makes inapplicable.
//
// This is the difference between "may this caller do it" and "does it
// apply here", and both have to hold before an action is advertised. The
// route table and the admission chain answer the first: they know which
// methods exist on a resource and which scopes reach them, and neither has
// any idea what state the particular resource is in. Only the payload
// does. A retired device still declares a DELETE route and a caller may
// still hold inventory:write, but deleting it again is not an available
// next step, and offering it would be a link a client can follow to no
// effect.
//
// This is also the clause that makes the pattern's name literal rather
// than decorative. Hypermedia as the Engine of Application *State* means
// the affordances change as the resource does; without this, _links would
// only ever be a server-side rendering of the caller's permission table,
// which is the thing PATTERNS.md's Backend for Frontend entry rejects
// duplicating.
//
// Implementing it is optional. A payload that does not is treated as
// allowing every relation the caller is admitted for.
type LinkFilter interface {
	AllowsRel(rel auth.LinkRel) bool
}

// LinkSet is the embeddable field every linkable response DTO carries.
//
// The pointer is load bearing and is not an ornament. It gives the wire
// three distinguishable states rather than two:
//
//   - absent: the affordances could not be computed at all, because the
//     authorization backend failed. A client must not read this as "you
//     may do nothing."
//   - []: computed successfully, and this caller may do nothing here.
//   - [ ... ]: computed successfully, and these are the actions available.
//
// FAILURE_PATTERNS.md #73 is the entry for what happens when a wire format
// cannot tell the first two apart: an outage becomes indistinguishable
// from a denial, and every consumer silently believes the denial. A plain
// []Link with omitempty would collapse the first two back together, since
// encoding/json omits a nil slice and an empty one alike.
type LinkSet struct {
	Links *[]Link `json:"_links,omitempty"`
}

// SetLinks implements Linkable.
func (l *LinkSet) SetLinks(links []Link) { l.Links = &links }

// filterFor returns the relation predicate payload declares, or one that
// allows everything when it declares none.
func filterFor(payload Linkable) func(auth.LinkRel) bool {
	if f, ok := payload.(LinkFilter); ok {
		return f.AllowsRel
	}
	return func(auth.LinkRel) bool { return true }
}

// errorResponse is the body every failure carries. It is deliberately one
// opaque message and never an internal error string: the /readyz precedent
// (health.go) and RequireScope's own no-detail rule both apply, since a
// reason can itself disclose which resources or scopes exist.
type errorResponse struct {
	Error string `json:"error"`
}

// Respond writes payload as JSON with the _links array computed for the
// route r matched and the identity AuthMiddleware placed in its context.
//
// Everything that can fail happens before the first byte reaches the
// client: links are computed, then the body is marshaled, and only then is
// a status line written. That ordering is FAILURE_PATTERNS.md #32's lesson
// applied structurally rather than remembered, since the first write to an
// http.ResponseWriter is the point of no return for the status code.
func Respond(w http.ResponseWriter, r *http.Request, status int, payload Linkable) {
	logger := loggerFrom(r)

	if builder, ok := linkBuilderFromContext(r.Context()); ok {
		// An unauthenticated caller has no affordances rather than an
		// error: this is reachable in tests and on any router built with
		// AllowUnauthenticated.
		id, _ := IdentityFromContext(r.Context())

		links, err := builder.linksFor(r, id, filterFor(payload))
		if err != nil {
			// Leave the field absent, which is the "could not be
			// computed" state, and say so loudly on the server side. The
			// response is still served: a device's own data is not
			// wrong just because its affordances are unknown.
			logger.ErrorContext(r.Context(), "failed to compute hypermedia affordances",
				slog.String("route", routePattern(r)),
				slog.String("error", err.Error()))
		} else {
			payload.SetLinks(links)
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		// Nothing is on the wire yet, so this can still be an honest 500
		// rather than a truncated body under a 200 status line.
		logger.ErrorContext(r.Context(), "failed to marshal response payload",
			slog.String("route", routePattern(r)),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSONBytes(w, r, status, body, logger)
}

// RespondError writes an opaque JSON error body with the given status.
//
// It carries no _links, deliberately. A caller that did not reach the
// resource has no affordances against it worth naming, and enumerating
// relations on a 403 would hand an unauthorized caller a map of what
// exists. This is the same reasoning /readyz already applies to dependency
// names on an unauthenticated endpoint.
func RespondError(w http.ResponseWriter, r *http.Request, status int, message string) {
	logger := loggerFrom(r)
	body, err := json.Marshal(errorResponse{Error: message})
	if err != nil {
		// errorResponse is one string field, so this is unreachable
		// short of a runtime failure. Fall back to the standard library
		// rather than recursing into this function.
		http.Error(w, message, status)
		return
	}
	writeJSONBytes(w, r, status, body, logger)
}

// writeJSONBytes commits an already-marshaled body: headers, then the
// status line, then the bytes, in that order and each exactly once.
//
// Content-Length is set because the body is already in hand, which lets a
// client size the response and lets a short write be detected below.
func writeJSONBytes(w http.ResponseWriter, r *http.Request, status int, body []byte, logger *slog.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)

	n, err := w.Write(body)
	if err != nil {
		// Past this point nothing can be told to the client, but an
		// operator still needs to know the response was truncated. This
		// is also what retires the four G104 gosec waivers the deleted
		// middleware carried: the error is handled, not ignored.
		logger.ErrorContext(r.Context(), "failed to write response body",
			slog.String("route", routePattern(r)),
			slog.Int("wrote", n),
			slog.Int("want", len(body)),
			slog.String("error", err.Error()))
		return
	}
	if n != len(body) {
		logger.ErrorContext(r.Context(), "short write on response body",
			slog.String("route", routePattern(r)),
			slog.Int("wrote", n),
			slog.Int("want", len(body)))
	}
}

// writeJSON writes v as a JSON body with the given status code, with no
// hypermedia links and no request context.
//
// It exists for the operational endpoints only. /healthz and /readyz are
// unversioned contracts with the orchestrator and the scrape agent rather
// than API resources, so they carry no affordances, take no identity, and
// must keep answering even when the rest of the API cannot.
//
// The encode error is deliberately ignored: by the time it could fire the
// status line and headers are already on the wire, so there is nothing
// left to tell the client, and every value passed here is a struct of
// strings that cannot fail to marshal.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// loggerFrom returns the logger a response should report failures through.
// It prefers the one StructuredLoggerMiddleware installed for this request
// and falls back to the process default, so a handler called directly by a
// test still logs somewhere rather than panicking on a nil.
func loggerFrom(r *http.Request) *slog.Logger {
	if logger, ok := loggerFromContext(r.Context()); ok {
		return logger
	}
	return slog.Default()
}
