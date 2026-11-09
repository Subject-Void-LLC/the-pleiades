// This file is the one place a value chi routed is read. Every handler in
// this package and in internal/ui/web reads a path parameter through
// PathParam or a helper over it, never through chi.URLParam.
//
// internal/archtest's TestOnlyPathParamReadsARoutedValue holds that. In
// every non-test file of the module it refuses, by name, chi's URLParam,
// URLParamFromCtx, URLParams and RouteCtxKey outside this file. In every
// package under internal/api, internal/ui and cmd/controller it refuses,
// by type, a read of a *url.URL's Path, RawPath, EscapedPath or RequestURI,
// an *http.Request's RequestURI or PathValue, and a *chi.Context's
// RoutePath, except at the sites its allowedReads names with a reason
// (three of them in this file: routedValue, routedPath and SentPath). It
// does not see reflection or a URL turned into text first.
//
// The reason is chi's own behavior, pinned by tests here: chi v5 matches a
// route against r.URL.RawPath whenever the request has one and records each
// value from that string, still escaped. A handler reading chi.URLParam for
// /inventory/devices/sw%2F1 got the literal "sw%2F1", so a device named
// "sw/1" had no URL and a link to it named another record (FAILURE_PATTERNS
// 1012 and 1013).
package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PathParam is the value of the route parameter key, decoded once, so it
// names the record the request named.
//
// It returns an error only for a value holding an escape that does not
// decode. net/http answers 400 itself to such a path before any handler
// runs, so through a real server this cannot happen; a caller answers it
// with 400 all the same (pathParam does), never 404. A 404 says no such
// record exists, which only a lookup may say, and a client reading it as
// "absent" could act on that (create the record again, drop it from a
// sync). The value is never echoed: it is the caller's text.
//
// A key the matched route does not declare reads as "", as chi.URLParam's
// does.
func PathParam(r *http.Request, key string) (string, error) {
	value, err := routedValue(r, chi.URLParam(r, key))
	if err != nil {
		return "", fmt.Errorf("api: route parameter %s: %w", key, err)
	}
	return value, nil
}

// pathParam is PathParam for a handler in this package: it answers an
// undecodable value with 400 itself and reports false.
func pathParam(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	value, err := PathParam(r, key)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "invalid path parameter")
		return "", false
	}
	return value, true
}

// pathID is the route parameter key read as a positive integer, the shape
// of every numeric record id this API serves. The caller words its own 400,
// since it knows which id it was reading.
func pathID(r *http.Request, key string) (int, error) {
	raw, err := PathParam(r, key)
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if id < 1 {
		return 0, fmt.Errorf("api: route parameter %s is not a positive integer", key)
	}
	return id, nil
}

// pathUUID is the route parameter key, decoded, when it parses as a UUID,
// the shape of every job id. It returns the value as given rather than
// re-formatted, so a lookup sees what the caller sent.
func pathUUID(r *http.Request, key string) (string, error) {
	raw, err := PathParam(r, key)
	if err != nil {
		return "", err
	}
	if _, err := uuid.Parse(raw); err != nil {
		return "", err
	}
	return raw, nil
}

// routeParam is one parameter of the route a request matched, decoded.
type routeParam struct {
	key   string
	value string
}

// routeParams is every parameter of the route r matched, decoded once, in
// the order chi recorded them, for building a link from the route's
// pattern (hrefFor). chi's "*" key, which it records for a mounted
// subrouter's remainder, is left out: it is a path, not a value.
func routeParams(r *http.Request) ([]routeParam, error) {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return nil, nil
	}
	keys, values := rctx.URLParams.Keys, rctx.URLParams.Values
	if len(keys) != len(values) {
		return nil, fmt.Errorf("api: route context has %d parameter keys but %d values", len(keys), len(values))
	}
	params := make([]routeParam, 0, len(keys))
	for i, key := range keys {
		if key == "*" {
			continue
		}
		value, err := routedValue(r, values[i])
		if err != nil {
			return nil, fmt.Errorf("api: route parameter %s: %w", key, err)
		}
		params = append(params, routeParam{key: key, value: value})
	}
	return params, nil
}

// routedValue is a value chi recorded, decoded, so escaping it once names
// the same record the request did.
//
// chi matches a route against r.URL.RawPath whenever the request has one,
// which it does exactly when the path's escaping differs from the default
// (a name holding a / or a ;, or a client escaping more than Go would), and
// records each value from that string, still escaped. Otherwise it matches
// r.URL.Path, and the value is already decoded. Decoding only in the first
// case keeps a literal % in a decoded value from being read as an escape:
// a record named "a%2Fb" is never read as "a/b".
func routedValue(r *http.Request, value string) (string, error) {
	if r.URL.RawPath == "" {
		return value, nil
	}
	return url.PathUnescape(value)
}

// SentPath is u's path as the client escaped it, which is what chi
// matched: RawPath when the request had one, and otherwise EscapedPath,
// which is then the request's own spelling.
//
// url.URL.EscapedPath alone is not that. It ignores a RawPath that is not a
// valid escaping, one holding a raw |, ^, backtick, {, }, < or >, and
// escapes the decoded path afresh, in which each %2F has become a
// separator: /devices/a%2Fb|c, one segment to chi, reads /devices/a/b%7Cc.
// SentPath keeps the RawPath and percent-encodes only such raw bytes
// (escapeRawBytes), so it is always a valid escaping, with chi's segments,
// that decodes to u.Path.
func SentPath(u *url.URL) string {
	if u.RawPath != "" {
		return escapeRawBytes(u.RawPath)
	}
	return u.EscapedPath()
}

// escapeRawBytes percent-encodes each byte of an escaped path that a valid
// escaping cannot hold raw, leaving every existing escape, every / and
// every byte RFC 3986 allows in a path segment as it is.
func escapeRawBytes(p string) string {
	const keep = "-._~!$&'()*+,;=:@/%"
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// routedPath is the path chi matches a request against: r.URL.RawPath when
// the request has one, else r.URL.Path (chi's Mux.routeHTTP). Split on /,
// it has the segments chi saw, so a name holding %2F stays one segment.
func routedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	return r.URL.Path
}
