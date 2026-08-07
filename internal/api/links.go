// This file owns the one mechanism behind both hypermedia renderings this
// API emits: the _links array in a response body (respond.go) and the
// Allow header on an OPTIONS or 405 response (options.go). They read the
// same route table, ask the same auth.HATEOASGenerator, and build hrefs
// the same way, so the two cannot disagree about what a caller may do.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/go-chi/chi/v5"
)

// affordance is one route's hypermedia projection: everything needed to
// render a link, plus the authorization question that decides whether to
// render it at all.
type affordance struct {
	rel    auth.LinkRel
	method string
	scope  auth.Scope
}

// linkBuilder maps a chi route pattern to every affordance registered on
// it, and holds the generator that decides which of them a given caller
// may exercise.
//
// It is built once, in NewRouter, and never mutated afterwards. Every
// field below is read concurrently by every in-flight request, so a lazy
// cache or a per-request memo here would be a data race rather than an
// optimization. If this ever needs memoization, it belongs in a
// request-scoped value, not here.
type linkBuilder struct {
	byPattern map[string][]affordance
	generator auth.HATEOASGenerator
}

// newLinkBuilder indexes routes by pattern so a request costs one map
// lookup plus one admission probe per affordance on its own resource,
// rather than a scan of the whole table.
func newLinkBuilder(routes []Route, generator auth.HATEOASGenerator) *linkBuilder {
	byPattern := make(map[string][]affordance, len(routes))
	for _, route := range routes {
		full := APIVersionPrefix + route.Pattern
		byPattern[full] = append(byPattern[full], affordance{
			rel:    route.Rel,
			method: route.Method,
			scope:  route.Scope,
		})
	}
	return &linkBuilder{byPattern: byPattern, generator: generator}
}

// linkBuilderContextKey is the private key the builder travels under.
type linkBuilderContextKey struct{}

// withLinkBuilder returns a middleware that puts b in the request context.
//
// The builder rides the context rather than being injected into every
// handler constructor because the thing it is keyed on, the matched chi
// route pattern, is already context-borne. Introducing no new mechanism
// keeps Respond callable from any handler, including one a test constructs
// directly.
func withLinkBuilder(b *linkBuilder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), linkBuilderContextKey{}, b)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// linkBuilderFromContext returns the builder withLinkBuilder placed in ctx,
// if any. Absence is not an error: a handler invoked directly by a test,
// or mounted outside NewRouter, still has to be able to respond.
func linkBuilderFromContext(ctx context.Context) (*linkBuilder, bool) {
	b, ok := ctx.Value(linkBuilderContextKey{}).(*linkBuilder)
	return b, ok && b != nil
}

// candidatesFor returns every affordance declared on pattern, in
// registration order, so a caller's link order is stable across requests
// rather than dependent on map iteration.
func (b *linkBuilder) candidatesFor(pattern string) []affordance {
	return b.byPattern[pattern]
}

// linksFor returns the hypermedia links id may exercise against the
// resource r matched, or an error if the decision could not be reached.
//
// The error is not cosmetic. FAILURE_PATTERNS.md #73 records that the
// deleted middleware discarded exactly this error, which turned an
// authorization-backend outage into a silent "you may do nothing" that a
// client could not tell apart from a correctly-evaluated caller with no
// permissions. Respond distinguishes them on the wire; this method's job
// is to keep the distinction reachable.
// applies is the resource's own predicate: it reports whether an
// affordance the caller is authorized for is actually available given the
// state of this particular resource. See LinkFilter (respond.go) for why
// authorization alone is not enough to decide whether to advertise an
// action.
func (b *linkBuilder) linksFor(r *http.Request, id *auth.Identity, applies func(auth.LinkRel) bool) ([]Link, error) {
	pattern := routePattern(r)
	candidates := b.candidatesFor(pattern)
	if len(candidates) == 0 {
		// An unrouted path has no affordances. Emitting a self link here
		// would name a URL this router does not serve.
		return []Link{}, nil
	}

	offered := make([]auth.Affordance, 0, len(candidates))
	for _, c := range candidates {
		offered = append(offered, auth.Affordance{Rel: c.rel, Scope: c.scope})
	}

	permittedRels, err := b.generator.Permitted(r.Context(), id, offered)
	if err != nil {
		return nil, fmt.Errorf("computing affordances for %s: %w", pattern, err)
	}

	// Re-intersect against our own candidates rather than trusting the
	// generator's answer directly. The port returns relation names
	// precisely so an implementation cannot invent one, and this is where
	// that guarantee is actually enforced: a relation we never offered
	// never reaches a response body, whatever a third-party generator
	// returns.
	permitted := make(map[auth.LinkRel]bool, len(permittedRels))
	for _, rel := range permittedRels {
		permitted[rel] = true
	}

	href, err := hrefFor(r, pattern)
	if err != nil {
		return nil, err
	}

	links := make([]Link, 0, len(candidates))
	for _, c := range candidates {
		// Both gates must pass. permitted answers "may this caller",
		// applies answers "does it apply to this resource in its current
		// state", and an action needs both to be a real next step rather
		// than a link that resolves to nothing.
		if !permitted[c.rel] {
			continue
		}
		if applies != nil && !applies(c.rel) {
			continue
		}
		links = append(links, Link{Rel: c.rel, Href: href, Method: c.method})
	}
	return links, nil
}

// allowedMethods returns the sorted set of HTTP methods id may use against
// the resource r matched, always including OPTIONS.
//
// This is the Allow header of PLAN.md Section 21.2, and it is deliberately
// computed from linksFor's own inputs rather than from chi's route table.
// chi's built-in 405 handler emits every method registered on a pattern,
// unfiltered by permission, which is the behavior Section 21.2 exists to
// improve on.
func (b *linkBuilder) allowedMethods(r *http.Request, id *auth.Identity, pattern string) ([]string, error) {
	candidates := b.candidatesFor(pattern)

	// OPTIONS is always permitted on a pattern this router serves: it is
	// the discovery primitive itself, and refusing to answer "what may I
	// do here" tells a caller nothing it could act on.
	methods := []string{http.MethodOptions}
	if len(candidates) == 0 {
		return methods, nil
	}

	offered := make([]auth.Affordance, 0, len(candidates))
	for _, c := range candidates {
		offered = append(offered, auth.Affordance{Rel: c.rel, Scope: c.scope})
	}

	permittedRels, err := b.generator.Permitted(r.Context(), id, offered)
	if err != nil {
		return nil, fmt.Errorf("computing allowed methods for %s: %w", pattern, err)
	}
	permitted := make(map[auth.LinkRel]bool, len(permittedRels))
	for _, rel := range permittedRels {
		permitted[rel] = true
	}

	for _, c := range candidates {
		if permitted[c.rel] {
			methods = append(methods, c.method)
		}
	}

	sortMethods(methods)
	return methods, nil
}

// hrefFor rebuilds the URL of the resource r matched from the server's own
// route pattern, substituting each URL parameter re-escaped.
//
// It never uses r.URL.Path. FAILURE_PATTERNS.md #72 records that the
// deleted middleware did, so every href it emitted was a value the caller
// chose rather than one the server serves. Here the structure is always
// the pattern's and only the parameter values come from the request, each
// escaped as a path segment, so a parameter containing a slash, a quote,
// or a percent cannot change the shape of the URL a client is invited to
// follow.
func hrefFor(r *http.Request, pattern string) (string, error) {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return pattern, nil
	}

	href := pattern
	for i, key := range rctx.URLParams.Keys {
		if i >= len(rctx.URLParams.Values) {
			return "", fmt.Errorf("api: route context for %s has %d parameter keys but %d values", pattern, len(rctx.URLParams.Keys), len(rctx.URLParams.Values))
		}
		// chi records a "*" key for a wildcard match. validateRoutes
		// refuses wildcard patterns outright, so reaching one here means
		// the request matched something this builder cannot describe.
		if key == "*" {
			continue
		}
		href = strings.ReplaceAll(href, "{"+key+"}", url.PathEscape(rctx.URLParams.Values[i]))
	}
	return href, nil
}

// sortMethods orders an Allow set alphabetically so one caller's header is
// byte-identical across requests. It is an insertion sort because the set
// is at most a handful of verbs.
func sortMethods(methods []string) {
	for i := 1; i < len(methods); i++ {
		for j := i; j > 0 && methods[j-1] > methods[j]; j-- {
			methods[j-1], methods[j] = methods[j], methods[j-1]
		}
	}
}
