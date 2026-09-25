// Package http implements the "http.request" namespaced Collection
// method, this platform's ansible.builtin.uri.
//
// This package's init registers "http.request" into the shared
// pkg/collection registry via collection.MustRegister. A runbook task
// naming this FQCN reaches engine.NewCollectionActionExecutor's real
// dispatch path (cmd/pleiades/run.go), which calls Request below
// directly, since this Manifest's Status is StatusImplemented and Invoke
// is set.
//
// # A URL, or a path on the device's own API
//
// This method opens no transport to the target. It runs where the task
// runs, from the CLI on the Crawl tier or from the runner on the Walk
// tier, and speaks to whatever URL the runbook names using net/http. That
// is why its manifest declares no transport and no capability: an
// arbitrary HTTP call has no device-side prerequisite to check, and
// declaring one would refuse tasks that are perfectly runnable.
//
// A url that is a path instead (request_device.go) calls the target
// device's own API: a generic_http device onboarding proved serves one.
// That is the one case the device matters, and the one case a credential
// is sent: the device's own, joined to its own origin.
//
// Because this package lives under internal/, it is reachable only from
// code inside this module or a fork of it: Go's internal/ visibility rule
// blocks any other module from importing it at all. A third-party
// Collection arriving from outside this binary needs a separate,
// not-yet-built distribution mechanism (Part X's OCI distribution work),
// not this one.
package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names, which are ansible.builtin.uri's own names. This
// platform is a superset of Ansible rather than a new vocabulary, so a
// person converting a playbook that calls an API should be renaming
// nothing.
//
// Every identifier in this package carries the request prefix because a
// sibling method landing in this namespace later would share the package,
// and Go has no file-level scope to keep two files' names apart.
const (
	requestParamURL           = "url"
	requestParamMethod        = "method"
	requestParamBody          = "body"
	requestParamHeaders       = "headers"
	requestParamStatusCode    = "status_code"
	requestParamTimeout       = "timeout"
	requestParamValidateCerts = "validate_certs"
)

// Stat names this method reports under, which are ansible.builtin.uri's
// own return names so a converted playbook's later tasks read what they
// already read.
//
// They are stats rather than facts, and that is the opposite call from
// facts.gather next door: a response body is true for as long as the run
// lasts and says nothing about the device a week from now, so keeping it
// as drift data would fill the fact store with answers nobody can compare
// against anything.
const (
	requestStatStatus  = "status"
	requestStatContent = "content"
	requestStatHeaders = "headers"
	requestStatURL     = "url"
	requestStatElapsed = "elapsed"
	requestStatMessage = "msg"
)

// Defaults, each matching ansible.builtin.uri's own.
//
// requestDefaultValidateCerts is the one that matters. It is true, and
// there is deliberately no setting, environment variable or deployment
// value anywhere that can change it: turning off certificate verification
// is a per-task decision written next to the request it applies to, where
// review can see it.
const (
	requestDefaultMethod        = nethttp.MethodGet
	requestDefaultStatusCode    = nethttp.StatusOK
	requestDefaultTimeout       = 30 * time.Second
	requestDefaultValidateCerts = true
)

// The lowest and highest numbers that are HTTP status codes at all, used
// to refuse a status_code that could never match a response.
const (
	requestMinStatusCode = 100
	requestMaxStatusCode = 599
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "http.request",
		Manifest: collection.Manifest{
			// Empty rather than absent, matching the catalog's own "none"
			// entry for this method: there is no transport to the device
			// because there is no device, and no capability to require
			// because an HTTP call has no device-side prerequisite.
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// False, and the reason is not "a GET changes nothing". This
			// field answers once for every invocation, and the two families
			// of invocation fail it from opposite directions: a read-only
			// verb has nothing to undo, and a writing verb's effect landed
			// on a system this platform cannot see. Neither can produce an
			// inverse, so nothing is emitted at run time either.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "One answer has to cover every verb, and no verb can produce an inverse. A read-only request (GET, HEAD, OPTIONS, " +
					"TRACE) changed nothing, so there is nothing to undo. A writing request changed something inside a remote system this " +
					"platform never observes: it cannot read what the state was before, cannot tell what the call altered, and cannot know " +
					"which call would put it back, so any inverse would be a guess dressed as an instruction.",
			},
			Doc: requestDoc(),
			// A check sends a read-only request for real, since reading is
			// all it does (CheckRequest), and cannot check any other.
			SupportsCheck: true,
		},
		Invoke:    Request,
		Check:     CheckRequest,
		CheckCall: requestCheckCall,
	})
}

// CheckRequest is http.request's check. A request in one of RFC 9110's
// safe methods (GET, HEAD, OPTIONS, TRACE, requestIsSafe) is defined as
// read-only, so a check sends it for real, through Request itself, and
// reports exactly what a real run would: the same status check, the same
// stats, no change. Any other method may change something on the server,
// which cannot be known without sending it, so the check answers that it
// cannot check this call (collection.CannotCheck) and sends nothing. A
// request a real run would refuse (a bad URL, a bad parameter) is refused
// here the same way, before either branch.
//
// The server's side of a safe request is the server's business: a server
// that changes state on a GET breaks HTTP's own contract, and a check
// cannot see that any more than a real run can.
func CheckRequest(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	if _, err := requestBuild(params, device); err != nil {
		return collection.Result{}, fmt.Errorf("http.request: %w", err)
	}
	if err := requestCheckCall(params); err != nil {
		return collection.Result{}, err
	}
	return Request(ctx, rc, device, params)
}

// requestCheckCall is the part of CheckRequest's answer params settle on
// their own (Descriptor.CheckCall): a request in any method but a safe one
// cannot be checked. Validation asks it before a run, so check_mode on a
// POST is refused when the runbook is written rather than reported
// unchecked when it runs.
func requestCheckCall(params map[string]any) error {
	if method := requestMethod(params); !requestIsSafe(method) {
		return collection.CannotCheck(fmt.Sprintf(
			"a %s request may change something on the server, and whether it would cannot be known without sending it", method))
	}
	return nil
}

// requestDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It is duplicated into internal/forge/catalogdata, which is the source
// the scaffolder is driven from, and internal/archtest's
// TestCatalogDataDocsMatchTheRegistry compares the two for equality so
// the copies cannot drift.
func requestDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes an HTTP request and reports its status code and body.",
		Description: "Calls a URL from wherever the task runs, not from the target device, and records the status, body and response headers. A response whose status is not one of the expected ones fails the task, after recording what came back, since the body is usually the only thing that explains the failure. Certificates are verified unless a task says otherwise in its own text. Reporting changed follows the verb: GET, HEAD, OPTIONS and TRACE are read-only by HTTP's own definition and report no change, while any other verb reports a change, because what it did to the far side cannot be inspected from here. That is a deliberate difference from ansible.builtin.uri, which never reports changed at all. Four of that module's parameters are absent rather than accepted and ignored: body_format (the body is sent exactly as written, so set Content-Type in headers), return_content (the body is always recorded), follow_redirects (redirects are always followed), and the url_username and url_password pair (a credential belongs in the credential store, not in a runbook file). Only a request in a safe method (GET, HEAD, OPTIONS or TRACE) can be checked, and a check sends it for real, since reading is all it does. Any other method may change something on the server, so such a call is named as unchecked and sends nothing, and check_mode on one is refused when the runbook is validated.",
		Params: []collection.Param{
			{Name: requestParamURL, Type: "string", Required: true, Description: "The URL to call. It must be http or https: any other scheme is refused rather than attempted, since this method speaks one protocol and a file or ftp URL is a mistake in the runbook rather than a request this could make. A path beginning with one / instead calls the target device's own API: it is joined to the base URL of a generic_http device that onboarding proved (HTTPAPICapable), and only such a request carries a credential, the device's own from the credential store, sent as its http_auth property says. It cannot leave that origin: a scheme, a host, a second leading / and a .. segment are refused, a redirect elsewhere is not followed, and the task may set neither Authorization nor Host."},
			{Name: requestParamMethod, Type: "string", Default: "GET", Description: "The HTTP method. It is upper-cased before being sent, because HTTP method names are case sensitive and a server given get will answer 501 rather than doing what the author meant."},
			{Name: requestParamBody, Type: "string", Description: "The request body, sent exactly as written. Set its Content-Type through headers: nothing here inspects the body or guesses a type for it."},
			{Name: requestParamHeaders, Type: "dict", Description: "Request headers as a mapping of name to value. Every value must be text, so a numeric one is quoted in the runbook. A Host header is honored as the request's real Host rather than added as an ordinary header, which is what makes name-based routing testable against an address."},
			{Name: requestParamStatusCode, Type: "int or list of int", Default: "200", Description: "The status code, or codes, that count as success. Anything else fails the task. Written as one number or a list of them, so an endpoint answering 200 or 201 depending on whether it created something can be accepted without a follow-up condition."},
			{Name: requestParamTimeout, Type: "float", Default: "30", Description: "How long to wait, in seconds, for the whole request including reading the body. Fractions are allowed, unlike Ansible's whole-second version, since a health check with a half-second budget is a real thing to want. Zero and negative values are refused: neither is a wait."},
			{Name: requestParamValidateCerts, Type: "bool", Default: "true", Description: "Verify the server's TLS certificate. Setting it false accepts any certificate, including one an attacker in the middle presents, so it belongs only on a target you have decided does not need it. There is no setting anywhere that changes the default: turning verification off is written in the task it applies to."},
		},
		Returns: []collection.ReturnField{
			{Name: requestStatStatus, Type: "int", Returned: "always", Description: "The status code the server answered with, recorded even when it is not one of the expected ones."},
			{Name: requestStatContent, Type: "string", Returned: "always", Description: "The whole response body as text. Held in memory, so this method is for calling an API rather than for fetching a large file."},
			{Name: requestStatHeaders, Type: "dict", Returned: "always", Description: "The response headers, names lower-cased, with a header sent more than once joined by a comma and a space."},
			{Name: requestStatURL, Type: "string", Returned: "always", Description: "The URL the response actually came from, which differs from the one asked for when redirects were followed."},
			{Name: requestStatElapsed, Type: "float", Returned: "always", Description: "How long the request took, in seconds, with its fraction kept. Ansible reports whole seconds here, which is zero for every call that went well."},
			{Name: requestStatMessage, Type: "string", Returned: "always", Description: "The status line, for example \"404 Not Found\", for a person reading a run log."},
		},
		Examples: []collection.Example{
			{
				Name:        "Check that a service answers",
				RunbookYAML: "- name: Wait for the health endpoint\n  http.request:\n    url: https://api.example.com/healthz\n    timeout: 5\n",
			},
			{
				Name:        "Post JSON to an API",
				RunbookYAML: "- name: Register the release\n  http.request:\n    url: https://api.example.com/releases\n    method: POST\n    body: '{\"version\": \"1.4.0\"}'\n    headers:\n      Content-Type: application/json\n    status_code:\n      - 200\n      - 201\n",
			},
			{
				Name:        "Call a device's own API with its stored credential",
				RunbookYAML: "- name: Read the device's interfaces\n  http.request:\n    url: /interfaces\n",
			},
		},
		SeeAlso: []string{"exec.command", "pleiades.builtin.wait.port"},
	}
}

// Request implements the "http.request" collection method: it calls a URL
// and records what came back.
//
// # The order of operations is the contract
//
// Everything the runbook got wrong is refused before the first packet, so
// a mistake in a task costs no connection and names the runbook rather
// than the server. Then the call is made, then the response is recorded,
// and only then is an unexpected status turned into a failure. Recording
// before failing is the part worth stating: the body of a 4xx is almost
// always the only thing that says why, and a method that threw it away on
// the way out would leave an operator with a number and nothing else.
//
// # What Changed means here
//
// HTTP already answers this. RFC 9110 calls GET, HEAD, OPTIONS and TRACE
// safe, meaning they are not meant to alter state, and this reports no
// change for them. Every other verb reports a change whenever the request
// completed, for the same reason exec.command reports one whenever the
// command ran: what happened on the far side cannot be inspected from
// here, so the honest answer is that something may well have. Ansible's
// uri module reports no change for anything at all, which is the wrong
// half of that trade.
func Request(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "http.request"

	spec, err := requestBuild(params, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	// The timeout wraps the caller's context rather than replacing it, so
	// a run that is being canceled still stops immediately, and it is
	// canceled only after the body has been read: a deadline that ended at
	// the response header would leave a slow body to hang forever.
	ctx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()

	req, err := spec.request(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if spec.device != nil {
		if err := httpapi.Authorize(req, spec.device.auth, rc.InjectSecrets()); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	started := time.Now()
	resp, err := spec.client().Do(req)
	elapsed := time.Since(started)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s %s: %w", fqcn, spec.method, spec.url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		// A body that stopped arriving partway is a failed request, not a
		// short one. Reporting the bytes that did arrive as the content
		// would hand a later condition a truncated document that parses.
		return collection.Result{}, fmt.Errorf("%s: %s %s: reading the response body: %w", fqcn, spec.method, spec.url, err)
	}

	if err := requestRecord(rc, resp, content, elapsed); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if !spec.accepts(resp.StatusCode) {
		return collection.Result{}, fmt.Errorf("%s: %s %s returned %d, wanted %s",
			fqcn, spec.method, spec.url, resp.StatusCode, spec.wanted())
	}

	return collection.Result{Changed: !requestIsSafe(spec.method)}, nil
}

// requestSpec is one fully checked request, ready to be built and sent.
//
// It exists so that every refusal happens in one place, before anything
// is opened, and so that Request itself reads as the sequence of steps it
// is rather than as parameter handling with a call buried in the middle.
type requestSpec struct {
	url           string
	method        string
	body          string
	headers       map[string]string
	statusCodes   []int
	timeout       time.Duration
	validateCerts bool

	// device is set when url is a path on the target device's API
	// (request_device.go), and nil for a full URL.
	device *requestDevice
}

// requestBuild reads and checks everything this method needs from the
// task's params.
// requestMethod is the HTTP method params ask for, GET when they name
// none.
func requestMethod(params map[string]any) string {
	if named := sdk.StringParam(params, requestParamMethod); named != "" {
		// Upper-cased rather than passed through. HTTP method names are
		// case sensitive, so a runbook saying get would be asking for a
		// method no server implements, and answering 501 to a task whose
		// author clearly meant GET helps nobody.
		return strings.ToUpper(named)
	}
	return requestDefaultMethod
}

func requestBuild(params map[string]any, device inventory.InventoryItem) (requestSpec, error) {
	var none requestSpec

	var target string
	var onDevice *requestDevice
	var err error
	if raw := sdk.StringParam(params, requestParamURL); requestIsDevicePath(raw) {
		target, onDevice, err = requestDeviceURL(device, raw)
	} else {
		target, err = requestURL(params)
	}
	if err != nil {
		return none, err
	}
	headers, err := requestHeaders(params)
	if err != nil {
		return none, err
	}
	statusCodes, err := requestStatusCodes(params)
	if err != nil {
		return none, err
	}
	timeout, err := requestTimeout(params)
	if err != nil {
		return none, err
	}
	validateCerts, err := sdk.BoolParamOr(params, requestParamValidateCerts, requestDefaultValidateCerts)
	if err != nil {
		return none, err
	}
	if onDevice != nil {
		if err := onDevice.check(headers, validateCerts); err != nil {
			return none, err
		}
	}

	return requestSpec{
		url:           target,
		method:        requestMethod(params),
		body:          sdk.StringParam(params, requestParamBody),
		headers:       headers,
		statusCodes:   statusCodes,
		timeout:       timeout,
		validateCerts: validateCerts,
		device:        onDevice,
	}, nil
}

// requestURL reads the one required parameter and refuses anything this
// method could not actually call.
//
// The scheme check is the reason this is not just a string read. Handing
// a file:// or ftp:// URL to net/http produces an error about an
// unsupported protocol scheme from somewhere deep in the transport, which
// names neither the task nor what to write instead.
func requestURL(params map[string]any) (string, error) {
	raw, err := sdk.RequiredStringParam(params, requestParamURL)
	if err != nil {
		return "", err
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%s %q cannot be read as a URL: %w", requestParamURL, raw, err)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("%s %q is %q, and this method speaks only http and https", requestParamURL, raw, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%s %q names no host", requestParamURL, raw)
	}
	return raw, nil
}

// requestHeaders reads the optional header mapping, refusing a value that
// is not text.
//
// A number is the case this catches. YAML reads an unquoted 8080 as an
// integer, and a header rendered from one with %v would be the text 8080
// on the Crawl tier and 8080.000 on the Walk tier, where the same value
// crossed a JSON boundary and came back a float. A module that sent two
// different requests depending on which tier ran it is worse than one
// that refuses.
func requestHeaders(params map[string]any) (map[string]string, error) {
	raw, present := params[requestParamHeaders]
	if !present || raw == nil {
		return nil, nil
	}

	mapping, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a mapping of header names to text values", requestParamHeaders)
	}

	headers := make(map[string]string, len(mapping))
	for name, value := range mapping {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%q] is %T, not text: quote it in the runbook", requestParamHeaders, name, value)
		}
		headers[name] = text
	}
	return headers, nil
}

// requestStatusCodes reads the status code, or codes, that count as
// success, defaulting to 200.
//
// One number and a list of numbers are both accepted because both are
// what people write: an endpoint that answers 200 or 201 depending on
// whether it created something needs the list, and everything else needs
// the number.
func requestStatusCodes(params map[string]any) ([]int, error) {
	raw, present := params[requestParamStatusCode]
	if !present || raw == nil {
		return []int{requestDefaultStatusCode}, nil
	}

	// A list is unwrapped into its elements, and anything else is treated
	// as a list of one, so the checking below happens in exactly one place.
	items, ok := raw.([]any)
	if !ok {
		items = []any{raw}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s is an empty list: leave it out to accept %d", requestParamStatusCode, requestDefaultStatusCode)
	}

	codes := make([]int, 0, len(items))
	for _, item := range items {
		number, ok := requestNumber(item)
		if !ok {
			return nil, fmt.Errorf("%s %v is %T, not a number", requestParamStatusCode, item, item)
		}
		code := int(number)
		if float64(code) != number || code < requestMinStatusCode || code > requestMaxStatusCode {
			return nil, fmt.Errorf("%s %v is not an HTTP status code: they run from %d to %d",
				requestParamStatusCode, item, requestMinStatusCode, requestMaxStatusCode)
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// requestTimeout reads how long to wait, in seconds, defaulting to
// Ansible's own 30.
//
// A fraction is allowed, which Ansible's whole-second version does not.
// A health check with a half-second budget is a real thing to want, and
// rounding 0.5 down to zero or up to one would both be wrong.
func requestTimeout(params map[string]any) (time.Duration, error) {
	raw, present := params[requestParamTimeout]
	if !present || raw == nil {
		return requestDefaultTimeout, nil
	}

	seconds, ok := requestNumber(raw)
	if !ok {
		return 0, fmt.Errorf("%s %v is %T, not a number of seconds", requestParamTimeout, raw, raw)
	}
	if seconds <= 0 {
		// Zero is refused rather than read as "no limit". A task that meant
		// no limit did not write a timeout at all, and a zero deadline would
		// fail the request before it opened a socket.
		return 0, fmt.Errorf("%s %v is not a wait: give a positive number of seconds", requestParamTimeout, raw)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// requestNumber reads a value that arrived as either of the two shapes a
// number can have here.
//
// YAML decodes an unquoted 200 into an int, and the same value crossing
// the Runner's per-task subprocess boundary as JSON comes back a float64.
// Both are the number the author wrote, and a reader that accepted only
// one of them would work on one tier and refuse on the other.
func requestNumber(raw any) (float64, bool) {
	switch value := raw.(type) {
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case float64:
		return value, true
	default:
		return 0, false
	}
}

// request builds the net/http request this spec describes.
func (s requestSpec) request(ctx context.Context) (*nethttp.Request, error) {
	var body io.Reader
	if s.body != "" {
		body = strings.NewReader(s.body)
	}

	req, err := nethttp.NewRequestWithContext(ctx, s.method, s.url, body)
	if err != nil {
		return nil, err
	}

	for name, value := range s.headers {
		// Host is a field on the request rather than an ordinary header:
		// net/http takes the Host header off the map and sends the URL's
		// host instead, so setting it the obvious way is a silent no-op.
		// Name-based routing is the whole reason anyone sets it.
		if strings.EqualFold(name, "Host") {
			req.Host = value
			continue
		}
		req.Header.Set(name, value)
	}
	return req, nil
}

// client returns the HTTP client this spec's request should be sent with.
//
// A fresh client and a fresh transport each time rather than a shared one,
// because the only thing that varies is certificate verification and a
// shared client carrying a pooled connection that skipped verification
// could hand it to a later task that asked for verification. One task's
// decision must not leak into another's.
//
// The verifying path used to return a bare &http.Client{}, which is not a
// fresh transport at all: a nil Transport means http.DefaultTransport, the
// process-wide shared one. That was wrong for a reason nothing here would
// have shown. DefaultTransport caps a TLS handshake at ten seconds
// (TLSHandshakeTimeout) and nothing in this method could raise it, so the
// timeout parameter documented above as "how long to wait for the whole
// request" silently could not buy a slow endpoint more than ten seconds to
// complete its handshake. A task against a loaded device, or over a link
// with real latency, failed at ten seconds having been told it had sixty.
//
// The handshake budget is the task's own now. The request context already
// bounds the whole call at exactly the same value, so this removes a
// second, hidden deadline rather than adding one: there is now one number
// that decides how long a request may take, and it is the one the operator
// set.
//
// The cost, stated rather than left to be discovered: a transport per
// request is a connection pool per request, so two http.request tasks
// against the same endpoint no longer reuse a connection and each pays its
// own handshake. That is not a tradeoff this function can avoid while the
// handshake budget varies per task, since a shared transport can carry only
// one such deadline for everybody. It is also what the skip-verify path has
// always done and what this function's own contract asks for: the point of
// a fresh client is that one task's certificate decision cannot reach
// another's connection.
func (s requestSpec) client() *nethttp.Client {
	transport := nethttp.DefaultTransport.(*nethttp.Transport).Clone()
	transport.TLSHandshakeTimeout = s.timeout

	if !s.validateCerts {
		transport.TLSClientConfig = &tls.Config{
			// Opt-in only, per task, never from a setting: see the
			// validate_certs parameter's own documentation above.
			InsecureSkipVerify: true, // #nosec G402 -- the task asked for it in its own text
			MinVersion:         tls.VersionTLS12,
		}
	}
	client := &nethttp.Client{Transport: transport}
	if s.device != nil {
		client.CheckRedirect = s.device.checkRedirect
	}
	return client
}

// accepts reports whether code is one of the statuses this task counts as
// success.
func (s requestSpec) accepts(code int) bool {
	for _, wanted := range s.statusCodes {
		if wanted == code {
			return true
		}
	}
	return false
}

// wanted renders the accepted statuses for an error message, so the
// failure says what would have been accepted rather than only what
// arrived.
func (s requestSpec) wanted() string {
	if len(s.statusCodes) == 1 {
		return strconv.Itoa(s.statusCodes[0])
	}

	codes := make([]string, 0, len(s.statusCodes))
	for _, code := range s.statusCodes {
		codes = append(codes, strconv.Itoa(code))
	}
	return "one of " + strings.Join(codes, ", ")
}

// requestIsSafe reports whether method is one HTTP itself defines as
// read-only, which is what decides whether this task reports a change.
//
// The list is RFC 9110's, not a judgement call: GET, HEAD, OPTIONS and
// TRACE are the safe methods. Everything else, including the ones nobody
// thinks of as destructive, may have altered something on the far side
// that this platform cannot see.
func requestIsSafe(method string) bool {
	switch method {
	case nethttp.MethodGet, nethttp.MethodHead, nethttp.MethodOptions, nethttp.MethodTrace:
		return true
	default:
		return false
	}
}

// requestRecord writes the stats this method returns.
//
// They describe the response that actually arrived, which is why this
// runs before the status check rather than after it: a task that failed
// on an unexpected status is one whose body is the only thing that
// explains why, and it would be gone by the time anyone asked.
func requestRecord(rc sdk.RunbookContext, resp *nethttp.Response, content []byte, elapsed time.Duration) error {
	headers := make(map[string]any, len(resp.Header))
	for name, values := range resp.Header {
		// Lower-cased and joined, the way Ansible flattens response
		// headers, so a header sent twice is one readable value rather
		// than a list a CEL condition has to index into.
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}

	for key, value := range map[string]any{
		requestStatStatus:  resp.StatusCode,
		requestStatContent: string(content),
		requestStatHeaders: headers,
		// The response's own URL rather than the one asked for, so a task
		// that followed a redirect records where the answer came from.
		requestStatURL:     resp.Request.URL.String(),
		requestStatElapsed: elapsed.Seconds(),
		requestStatMessage: resp.Status,
	} {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	return nil
}
