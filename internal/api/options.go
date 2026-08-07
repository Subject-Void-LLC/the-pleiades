// This file implements PLAN.md Section 21.2's OPTIONS pre-flight: every
// endpoint answers OPTIONS, and the Allow header it returns lists only the
// methods this particular caller is authorized to use.
//
// That last clause is the whole point and is what chi's own behavior does
// not provide. chi's built-in 405 handler emits every method registered on
// a pattern, unfiltered by permission, and chi.Context.methodsAllowed is
// unexported so a middleware cannot even read the set to filter it. Both
// handlers below therefore derive the method set from this router's own
// route table (links.go), which is the only place method, pattern, and
// required scope are co-located.
package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// allowHeader is the response header carrying the permitted method set.
const allowHeader = "Allow"

// optionsHandler answers an OPTIONS request for one route pattern.
//
// It is registered per pattern inside APIVersionPrefix, so cfg.Auth runs
// in front of it and an anonymous caller gets 401. It is deliberately not
// wrapped in RequireScope: there is no single scope OPTIONS could require,
// and a viewer asking "what may I do here" must receive
// "Allow: GET, OPTIONS" rather than 403. Answering 403 would force exactly
// the hardcoded client-side permission table Section 21.2 exists to
// remove.
func optionsHandler(builder *linkBuilder, pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The identity is optional here only because a router built with
		// AllowUnauthenticated has no Auth middleware to supply one. On a
		// real deployment this is always present, because cfg.Auth
		// rejected the request otherwise.
		id, _ := IdentityFromContext(r.Context())

		methods, err := builder.allowedMethods(r, id, APIVersionPrefix+pattern)
		if err != nil {
			loggerFrom(r).ErrorContext(r.Context(), "failed to compute allowed methods",
				slog.String("route", APIVersionPrefix+pattern),
				slog.String("error", err.Error()))
			RespondError(w, r, http.StatusInternalServerError, "internal error")
			return
		}

		writeAllow(w, methods)

		// 204 rather than 200 with a body: the Allow header is the entire
		// contract, and a body here would invite a client to parse a
		// second, redundant representation of it that could drift.
		w.WriteHeader(http.StatusNoContent)
	}
}

// methodNotAllowedHandler answers a request whose path matched a route
// pattern but whose method did not, with the same scope-filtered Allow set
// OPTIONS would return.
//
// Replacing chi's built-in handler is what keeps the two answers
// consistent. Left alone, chi would tell a read-only caller that DELETE is
// available here, contradicting the _links array and the OPTIONS response
// the same caller just received.
func methodNotAllowedHandler(builder *linkBuilder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := IdentityFromContext(r.Context())

		// chi does not populate a RoutePattern for a 405, since no route
		// matched, so recover the pattern by asking the route table which
		// of its patterns this path corresponds to.
		pattern := matchPattern(builder, r.URL.Path)
		if pattern == "" {
			RespondError(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		methods, err := builder.allowedMethods(r, id, pattern)
		if err != nil {
			loggerFrom(r).ErrorContext(r.Context(), "failed to compute allowed methods",
				slog.String("route", pattern),
				slog.String("error", err.Error()))
			RespondError(w, r, http.StatusInternalServerError, "internal error")
			return
		}

		writeAllow(w, methods)
		RespondError(w, r, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// writeAllow emits the permitted set as a single comma-joined header value
// per RFC 9110 Section 10.2.1.
//
// One header, not N. chi's own handler calls Header().Add in a loop, which
// emits a separate Allow line per method; both spellings are legal but a
// single value is the conventional one and is what a client parsing with a
// simple split expects.
func writeAllow(w http.ResponseWriter, methods []string) {
	w.Header().Set(allowHeader, strings.Join(methods, ", "))
}

// matchPattern resolves a request path back to one of the router's own
// registered patterns.
//
// It exists because chi populates no RoutePattern on the 405 path: no
// route matched, so there is nothing for it to report. Asking chi to
// re-match is not an option either, since the handler runs on the
// versioned subrouter, whose own routing context works in paths relative
// to the mount point while this builder is keyed on full patterns.
//
// Matching segment by segment against the table this package already
// validated is both simpler and stricter than either. validateRoutes
// rejects wildcard patterns outright, so every pattern here has a fixed
// segment count and every non-literal segment is a single "{name}"
// placeholder, which makes this comparison total rather than a
// re-implementation of a routing algorithm.
func matchPattern(builder *linkBuilder, path string) string {
	pathSegments := splitPath(path)
	for pattern := range builder.byPattern {
		if patternMatches(splitPath(pattern), pathSegments) {
			return pattern
		}
	}
	return ""
}

// splitPath splits a URL path or route pattern into its segments,
// discarding the empty segments a leading or trailing slash produces.
func splitPath(p string) []string {
	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// patternMatches reports whether path satisfies pattern, treating any
// "{...}" segment as matching exactly one non-empty segment.
func patternMatches(pattern, path []string) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i, seg := range pattern {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			continue
		}
		if seg != path[i] {
			return false
		}
	}
	return true
}

// registerOptions mounts one OPTIONS handler per distinct route pattern,
// plus the scope-filtered 405 handler, on the versioned API subtree.
//
// Distinct patterns, not distinct routes: OPTIONS describes a resource,
// and two routes on one pattern are two methods of one resource.
func registerOptions(api chi.Router, builder *linkBuilder, routes []Route) {
	seen := make(map[string]bool, len(routes))
	for _, route := range routes {
		if seen[route.Pattern] {
			continue
		}
		seen[route.Pattern] = true
		api.Options(route.Pattern, optionsHandler(builder, route.Pattern))
	}
	if len(routes) > 0 {
		api.MethodNotAllowed(methodNotAllowedHandler(builder))
	}
}
