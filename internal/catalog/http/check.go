// http.check: whether a URL answers with the status, and optionally the
// body, a runbook expects, without ever returning or logging the body.
package http

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnCheck = "http.check"

// Parameters beside http.request's url, status_code, timeout and
// validate_certs, which http.check reads the same way.
const (
	checkParamBodyContains = "body_contains"
	checkParamBodyMatches  = "body_matches"
	checkParamMaxRedirects = "max_redirects"
)

// Bounds.
const (
	checkMaxBody             = 4 << 20
	checkDefaultMaxRedirects = 5
	checkMostRedirects       = 10
	checkMaxPattern          = 1024
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnCheck,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceNone},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It sends a GET or a HEAD, which HTTP defines as safe, and changes nothing a later task could undo.",
			},
			Doc: checkDoc(),
		},
		Invoke: Check,
		// A GET or a HEAD only reads, so a check of it is the task itself.
		Check: Check,
	})
}

// checkSpec is one call's parameters.
type checkSpec struct {
	url           *url.URL
	method        string
	statusCodes   []int
	contains      string
	matches       *regexp.Regexp
	maxRedirects  int
	timeout       time.Duration
	validateCerts bool
}

// checkParse reads a call, refusing a URL that carries a credential: it
// would reach every log the URL does.
func checkParse(params map[string]any) (checkSpec, error) {
	var s checkSpec
	_, parsed, err := requestURL(params)
	if err != nil {
		return s, err
	}
	s.url = parsed
	if s.url.User != nil {
		return s, fmt.Errorf("%s carries a user name or password, which would reach every log the URL does", requestParamURL)
	}
	switch s.method = strings.ToUpper(sdk.StringParam(params, requestParamMethod)); s.method {
	case "":
		s.method = nethttp.MethodGet
	case nethttp.MethodGet, nethttp.MethodHead:
	default:
		return s, fmt.Errorf("%s %s is not GET or HEAD: a check only reads", requestParamMethod, s.method)
	}
	if s.statusCodes, err = requestStatusCodes(params); err != nil {
		return s, err
	}
	if s.timeout, err = requestTimeout(params); err != nil {
		return s, err
	}
	if s.validateCerts, err = sdk.BoolParamOr(params, requestParamValidateCerts, requestDefaultValidateCerts); err != nil {
		return s, err
	}
	if s.contains, err = sdk.OptionalStringParam(params, checkParamBodyContains); err != nil {
		return s, err
	}
	pattern, err := sdk.OptionalStringParam(params, checkParamBodyMatches)
	if err != nil {
		return s, err
	}
	if pattern != "" {
		if s.contains != "" {
			return s, fmt.Errorf("give one of %s and %s", checkParamBodyContains, checkParamBodyMatches)
		}
		if len(pattern) > checkMaxPattern {
			return s, fmt.Errorf("%s is over %d bytes", checkParamBodyMatches, checkMaxPattern)
		}
		if s.matches, err = regexp.Compile(pattern); err != nil {
			return s, fmt.Errorf("%s: %w", checkParamBodyMatches, err)
		}
	}
	if (s.contains != "" || s.matches != nil) && s.method == nethttp.MethodHead {
		return s, errors.New("a HEAD answer has no body to match")
	}
	n, set, err := sdk.IntParam(params, checkParamMaxRedirects)
	switch {
	case err != nil:
		return s, err
	case !set:
		s.maxRedirects = checkDefaultMaxRedirects
	case n < 0 || n > checkMostRedirects:
		return s, fmt.Errorf("%s is %d; it must be from 0 to %d", checkParamMaxRedirects, n, checkMostRedirects)
	default:
		s.maxRedirects = n
	}
	return s, nil
}

// shown is a URL as a result names it: scheme, host and path, never its
// query or fragment, which can carry a token.
func shown(u *url.URL) string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// Check implements http.check.
func Check(ctx context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnCheck, err)
	}
	s, err := checkParse(params)
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	transport := nethttp.DefaultTransport.(*nethttp.Transport).Clone()
	transport.TLSHandshakeTimeout = s.timeout
	if !s.validateCerts {
		transport.TLSClientConfig = &tls.Config{
			// Opt-in only, per task, as http.request's validate_certs.
			InsecureSkipVerify: true, // #nosec G402 -- the task asked for it in its own text
			MinVersion:         tls.VersionTLS12,
		}
	}
	// Empty, not nil: an any holding a nil slice is not nil, so a check that
	// followed no redirect must return an empty list, not a typed nil.
	hops := []any{}
	client := &nethttp.Client{Transport: transport, CheckRedirect: func(req *nethttp.Request, via []*nethttp.Request) error {
		if via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
			return fmt.Errorf("a redirect from https to http (%s), which this does not follow", shown(req.URL))
		}
		// At the bound the redirect itself is the answer checked, so a check
		// can ask for a 301 with max_redirects: 0.
		if len(via) > s.maxRedirects {
			return nethttp.ErrUseLastResponse
		}
		hops = append(hops, shown(req.URL))
		return nil
	}}
	// The method and URL were both held to their forms already, so the
	// request is made from them directly.
	req := (&nethttp.Request{Method: s.method, URL: s.url, Header: nethttp.Header{}, Host: s.url.Host}).WithContext(ctx)
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return fail(fmt.Errorf("%s %s: %w", s.method, shown(s.url), stripURL(err)))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, checkMaxBody+1))
	if err != nil {
		return fail(fmt.Errorf("reading the answer: %w", err))
	}
	elapsed := time.Since(start)
	over := len(body) > checkMaxBody
	if over {
		body = body[:checkMaxBody]
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	stats := map[string]any{
		"status": resp.StatusCode, "final_url": shown(resp.Request.URL), "redirects": hops,
		"elapsed_ms": elapsed.Milliseconds(), "body_bytes": len(body), "body_sha256": digest, "body_over_limit": over,
	}
	var matched *bool
	if s.contains != "" || s.matches != nil {
		m := !over && ((s.contains != "" && strings.Contains(string(body), s.contains)) || (s.matches != nil && s.matches.Match(body)))
		matched = &m
		stats["matched"] = m
	}
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	bodyText := fmt.Sprintf("the body: %d bytes, sha256 %s", len(body), digest)
	switch {
	case !(requestSpec{statusCodes: s.statusCodes}).accepts(resp.StatusCode):
		return fail(fmt.Errorf("%s %s answered %d, wanted %s; %s", s.method, shown(s.url), resp.StatusCode, (requestSpec{statusCodes: s.statusCodes}).wanted(), bodyText))
	case matched != nil && over:
		return fail(fmt.Errorf("%s is over %d bytes, and is not matched against part of it", bodyText, checkMaxBody))
	case matched != nil && !*matched:
		return fail(fmt.Errorf("%s does not match", bodyText))
	}
	return collection.Result{}, nil
}

// stripURL removes the URL a transport error quotes, which can hold a
// query string, keeping only what went wrong.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func checkDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Checks that a URL answers with the expected status, and optionally body, without ever returning or logging the body.",
		Description: "Sends a GET (or a HEAD) to the URL, following at most max_redirects redirects and refusing one from https to http, since the check would then vouch for a page anyone on the path could have served. It fails when the status is not one of status_code, or when the body does not contain body_contains or match body_matches (RE2). The body is read, at most 4 MiB, only to match it: what is returned is the status, the final URL and each redirect (scheme, host and path, never a query or fragment, which can carry a token), the time taken, and the body's size and SHA-256, and a failure names the same. A URL carrying a user name or password is refused. Certificates are verified unless validate_certs is false, which is per task and loud. It only reads, so a check of it is the task itself, and it runs once with no target device.",
		Params: []collection.Param{
			{Name: requestParamURL, Type: "string", Required: true, Description: "The http or https URL to request."},
			{Name: requestParamStatusCode, Type: "int or list of int", Default: "200", Description: "The status, or statuses, that pass."},
			{Name: checkParamBodyContains, Type: "string", Description: "Text the body must contain."},
			{Name: checkParamBodyMatches, Type: "string", Description: "A regular expression (RE2, at most 1024 bytes) the body must match. One of body_contains and body_matches."},
			{Name: requestParamMethod, Type: "string", Default: "GET", Choices: []string{"GET", "HEAD"}, Description: "GET, or HEAD for the status alone."},
			{Name: checkParamMaxRedirects, Type: "int", Default: "5", Description: "How many redirects to follow, 0 to 10. A redirect past them is the answer checked, so max_redirects: 0 with status_code: 301 checks a redirect itself."},
			{Name: requestParamTimeout, Type: "int", Default: "30", Description: "Seconds the whole check may take, redirects included."},
			{Name: requestParamValidateCerts, Type: "bool", Default: "true", Description: "Verify the server's certificate. False is set per task, never by a setting, and is visible in the runbook that sets it."},
		},
		Returns: []collection.ReturnField{
			{Name: "status", Type: "int", Returned: "when the server answered", Description: "The final answer's status."},
			{Name: "final_url", Type: "string", Returned: "when the server answered", Description: "The URL that answered last, without query or fragment."},
			{Name: "redirects", Type: "list of string", Returned: "when the server answered", Description: "Each redirect followed, in order, without query or fragment."},
			{Name: "elapsed_ms", Type: "int", Returned: "when the server answered", Description: "How long the check took, in milliseconds."},
			{Name: "body_bytes", Type: "int", Returned: "when the server answered", Description: "How many bytes of body were read, at most 4 MiB."},
			{Name: "body_sha256", Type: "string", Returned: "when the server answered", Description: "The SHA-256 of the bytes read, in hex."},
			{Name: "body_over_limit", Type: "bool", Returned: "when the server answered", Description: "Whether the body was longer than 4 MiB."},
			{Name: "matched", Type: "bool", Returned: "with body_contains or body_matches", Description: "Whether the body matched."},
		},
		Examples: []collection.Example{
			{Name: "The release answers healthy", RunbookYAML: "- name: Health\n  http.check:\n    url: https://app.example.com/healthz\n    body_contains: \"\\\"status\\\":\\\"ok\\\"\"\n"},
		},
		SeeAlso: []string{"http.request", "wait.port"},
	}
}
