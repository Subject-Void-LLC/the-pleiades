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
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// affordance is one route's hypermedia projection: everything needed to
// render a link, plus the authorization question that decides whether to
// render it at all.
type affordance struct {
	rel    auth.LinkRel
	method string
	scope  auth.Scope

	// alsoScopes are the route's AlsoScopes: the affordance is offered only
	// to a caller admitted for its scope and every one of these.
	alsoScopes []auth.Scope
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
	// statics is, for each pattern, the fixed segments the route table
	// registers beside each of its parameters (staticSiblings).
	statics routeStatics
}

// newLinkBuilder indexes routes by pattern so a request costs one map
// lookup plus one admission probe per affordance on its own resource,
// rather than a scan of the whole table.
func newLinkBuilder(routes []Route, generator auth.HATEOASGenerator) *linkBuilder {
	byPattern := make(map[string][]affordance, len(routes))
	patterns := make([]string, 0, len(routes))
	for _, route := range routes {
		full := APIVersionPrefix + route.Pattern
		if _, seen := byPattern[full]; !seen {
			patterns = append(patterns, full)
		}
		byPattern[full] = append(byPattern[full], affordance{
			rel:        route.Rel,
			method:     route.Method,
			scope:      route.Scope,
			alsoScopes: route.AlsoScopes,
		})
	}
	return &linkBuilder{byPattern: byPattern, generator: generator, statics: staticSiblings(patterns)}
}

// routeStatics maps a route pattern to, for each of its parameters by name,
// the set of fixed segments another pattern registers at the same place.
type routeStatics map[string]map[string]map[string]bool

// staticSiblings is, for each pattern, every fixed segment another pattern
// registers where one of its parameters goes, under the same segments
// before it: "preview" for {id} of /schedules/{id}, from /schedules/preview.
// chi matches a fixed segment before a parameter, so a value equal to one
// would route to the other handler; hrefFor encodes its first byte instead
// (recordname.Segment). It reads the route table NewRouter registers, so a
// route added there is counted with no list kept by hand.
func staticSiblings(patterns []string) routeStatics {
	split := make([][]string, len(patterns))
	for i, p := range patterns {
		split[i] = strings.Split(strings.Trim(p, "/"), "/")
	}
	out := routeStatics{}
	for i, segs := range split {
		for j, seg := range segs {
			if !isParamSegment(seg) {
				continue
			}
			for k, other := range split {
				if k == i || len(other) <= j || isParamSegment(other[j]) || !samePrefix(segs[:j], other[:j]) {
					continue
				}
				if out[patterns[i]] == nil {
					out[patterns[i]] = map[string]map[string]bool{}
				}
				key := paramKey(seg)
				if out[patterns[i]][key] == nil {
					out[patterns[i]][key] = map[string]bool{}
				}
				out[patterns[i]][key][other[j]] = true
			}
		}
	}
	return out
}

// isParamSegment reports whether one pattern segment is a chi parameter.
func isParamSegment(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}

// paramKey is a parameter segment's name: {id} and {id:[0-9]+} are both id.
func paramKey(seg string) string {
	key := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
	if i := strings.IndexByte(key, ':'); i >= 0 {
		key = key[:i]
	}
	return key
}

// samePrefix reports whether two patterns' leading segments, of one length
// (staticSiblings slices both at the same place), name the same route node:
// equal fixed segments, and a parameter beside a parameter.
func samePrefix(a, b []string) bool {
	for i := range a {
		if isParamSegment(a[i]) && isParamSegment(b[i]) {
			continue
		}
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

	permitted, err := b.permitted(r, id, candidates)
	if err != nil {
		return nil, fmt.Errorf("computing affordances for %s: %w", pattern, err)
	}

	href, err := hrefFor(r, pattern, b.statics[pattern])
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

	permitted, err := b.permitted(r, id, candidates)
	if err != nil {
		return nil, fmt.Errorf("computing allowed methods for %s: %w", pattern, err)
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
func hrefFor(r *http.Request, pattern string, statics map[string]map[string]bool) (string, error) {
	// Every value decoded once (routeParams, pathparam.go), so escaping it
	// once below names the record the request named. chi's "*" key for a
	// wildcard match is left out there; validateRoutes refuses wildcard
	// patterns outright, so a link is never built from one.
	params, err := routeParams(r)
	if err != nil {
		return "", fmt.Errorf("api: building a link for %s: %w", pattern, err)
	}

	href := pattern
	for _, p := range params {
		href = strings.ReplaceAll(href, "{"+p.key+"}", recordname.Segment(p.value, statics[p.key]))
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

// permitted is the set of candidates' relations id may exercise: the
// generator's answer for each candidate's scope, and, for a candidate with
// alsoScopes, its answer for each of those too, since the router admits such
// a route only to a caller holding every one. One Permitted call per scope
// position, because the generator reports a relation permitted when any
// affordance offered for it is, which for two scopes would read as either
// rather than both.
//
// The answer is re-intersected against our own candidates rather than
// trusting the generator directly. The port returns relation names
// precisely so an implementation cannot invent one, and this is where that
// guarantee is enforced: a relation we never offered never reaches a
// response body or an Allow header, whatever a third-party generator
// returns.
func (b *linkBuilder) permitted(r *http.Request, id *auth.Identity, candidates []affordance) (map[auth.LinkRel]bool, error) {
	offered := make([]auth.Affordance, 0, len(candidates))
	positions := 0
	for _, c := range candidates {
		offered = append(offered, auth.Affordance{Rel: c.rel, Scope: c.scope})
		positions = max(positions, len(c.alsoScopes))
	}
	permitted, err := b.permittedSet(r, id, offered)
	if err != nil {
		return nil, err
	}
	for k := range positions {
		var also []auth.Affordance
		for _, c := range candidates {
			if k < len(c.alsoScopes) {
				also = append(also, auth.Affordance{Rel: c.rel, Scope: c.alsoScopes[k]})
			}
		}
		alsoPermitted, err := b.permittedSet(r, id, also)
		if err != nil {
			return nil, err
		}
		for _, c := range candidates {
			if k < len(c.alsoScopes) && !alsoPermitted[c.rel] {
				delete(permitted, c.rel)
			}
		}
	}
	return permitted, nil
}

// permittedSet asks the generator once and returns its answer as a set.
func (b *linkBuilder) permittedSet(r *http.Request, id *auth.Identity, offered []auth.Affordance) (map[auth.LinkRel]bool, error) {
	rels, err := b.generator.Permitted(r.Context(), id, offered)
	if err != nil {
		return nil, err
	}
	set := make(map[auth.LinkRel]bool, len(rels))
	for _, rel := range rels {
		set[rel] = true
	}
	return set, nil
}
