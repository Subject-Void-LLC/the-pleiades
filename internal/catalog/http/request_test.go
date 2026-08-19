// This file tests http.request against REAL HTTP servers, in this
// process, over real TCP and real TLS (net/http/httptest). Nothing about
// the protocol, the certificate chain, redirects or timeouts is stubbed.
//
// RULE 0 is why. This method's whole subject is what net/http does with
// the request it was handed, so a stubbed round tripper would only prove
// the stub returned what it was told to. The certificate tests in
// particular cannot be faked at all: httptest's TLS server presents a
// certificate no system trust store knows, which is exactly the
// difference between verifying and not.
//
// Every identifier here carries the request prefix, because a sibling
// method landing in this namespace later would share this test package
// and Go has no file-level scope.
package http_test

import (
	"context"
	"errors"
	"io"
	"log"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/http"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// errRequestStat is what a stub context returns when a test wants
// recording to fail, so the branch where the call succeeded and the
// record did not is reachable.
var errRequestStat = errors.New("recording the response failed")

// requestContext is a minimal sdk.RunbookContext keeping stats and facts
// in SEPARATE maps.
//
// The separation is an assertion, not bookkeeping. A response body is
// true for the length of this run and says nothing about a device next
// month, so it belongs in a stat; a context that folded the two together
// could not tell a method honoring that from one that emitted drift data
// nobody can compare against anything.
type requestContext struct {
	stats map[string]any
	facts map[string]any

	// failOn, when set, makes SetStat fail for that one key.
	failOn string
}

func newRequestContext() *requestContext {
	return &requestContext{stats: map[string]any{}, facts: map[string]any{}}
}

func (c *requestContext) InjectSecrets() map[string]string { return nil }

func (c *requestContext) SetStat(key string, value any) error {
	if c.failOn != "" && c.failOn == key {
		return errRequestStat
	}
	c.stats[key] = value
	return nil
}

func (c *requestContext) EmitFact(key string, value any) error {
	c.facts[key] = value
	return nil
}

// requestServer starts a real HTTP server for the duration of the test.
func requestServer(t *testing.T, handler nethttp.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// requestTLSServer starts a real HTTPS server presenting a certificate no
// system trust store knows, which is what makes certificate verification
// a real question rather than a configured one.
func requestTLSServer(t *testing.T, handler nethttp.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return server
}

// requestOK is the handler most tests use: a 200 with a small body and
// one header worth reading back.
func requestOK(w nethttp.ResponseWriter, _ *nethttp.Request) {
	w.Header().Set("X-Pleiades-Test", "yes")
	w.WriteHeader(nethttp.StatusOK)
	_, _ = io.WriteString(w, "hello")
}

// requestRun invokes the method with a nil device, which is itself part
// of the contract: this method opens no transport to a target, so it must
// work with no target at all.
func requestRun(rc *requestContext, params map[string]any) (collection.Result, error) {
	return http.Request(context.Background(), rc, nil, params)
}

// TestRequest_Registered proves the method registered itself as
// implemented, and that it answers the reversibility question the one way
// a single answer covering every verb can be answered.
//
// The Notes are checked for both halves of that reasoning. A note saying
// only "a GET changes nothing" would be an answer about one invocation
// rather than about the method, and it is the writing verbs that make the
// answer false for a different reason entirely.
func TestRequest_Registered(t *testing.T) {
	d, ok := collection.Lookup("http.request")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "http.request")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = true, but no verb here can produce an inverse")
	}
	notes := d.Manifest.Reversibility.Notes
	if notes == "" {
		t.Fatal("Reversibility.Notes is empty, which registration itself refuses")
	}
	for _, want := range []string{"GET", "nothing to undo", "remote system"} {
		if !strings.Contains(notes, want) {
			t.Errorf("Reversibility.Notes = %q, want it to explain %q too", notes, want)
		}
	}
}

// TestRequest_RecordsTheWholeResponse is the happy path, and it checks
// every stat rather than the status alone.
//
// The URL, the headers and the elapsed time are the ones a careless
// implementation gets wrong quietly: echoing the requested URL back,
// dropping the response headers, or reporting a whole-second elapsed that
// is zero for every call that went well.
func TestRequest_RecordsTheWholeResponse(t *testing.T) {
	const delay = 20 * time.Millisecond

	server := requestServer(t, func(w nethttp.ResponseWriter, r *nethttp.Request) {
		time.Sleep(delay)
		requestOK(w, r)
	})
	rc := newRequestContext()

	result, err := requestRun(rc, map[string]any{"url": server.URL + "/thing"})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if result.Changed {
		t.Error("a GET reported a change: HTTP itself calls it a safe method")
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts = %v, want none: a response expires with the run and belongs in a stat", rc.facts)
	}

	if got := rc.stats["status"]; got != 200 {
		t.Errorf("status stat = %v, want 200", got)
	}
	if got := rc.stats["content"]; got != "hello" {
		t.Errorf("content stat = %v, want the body the server wrote", got)
	}
	if got := rc.stats["msg"]; got != "200 OK" {
		t.Errorf("msg stat = %v, want the status line", got)
	}
	if got := rc.stats["url"]; got != server.URL+"/thing" {
		t.Errorf("url stat = %v, want the URL the response came from", got)
	}

	headers, ok := rc.stats["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers stat = %#v, want a map", rc.stats["headers"])
	}
	if got := headers["x-pleiades-test"]; got != "yes" {
		t.Errorf("headers stat = %v, want the response header under its lower-cased name", headers)
	}

	// A whole-second elapsed would be 0 here, which is the number Ansible
	// reports for every healthy call and the reason this one keeps its
	// fraction.
	elapsed, ok := rc.stats["elapsed"].(float64)
	if !ok {
		t.Fatalf("elapsed stat = %#v, want a float64 of seconds", rc.stats["elapsed"])
	}
	if elapsed < delay.Seconds() {
		t.Errorf("elapsed stat = %v, want at least the %v the handler slept", elapsed, delay)
	}
}

// TestRequest_SendsWhatTheTaskWrote proves the method, body and headers
// reach the server as written, by asserting on what the SERVER received
// rather than on what the method reported about itself.
//
// The Host header is the one worth singling out. net/http drops a Host
// set the ordinary way and sends the URL's host instead, so a method that
// treated it as just another header would produce a silent no-op, and
// name-based routing would never work.
func TestRequest_SendsWhatTheTaskWrote(t *testing.T) {
	var (
		gotMethod string
		gotBody   string
		gotType   string
		gotHost   string
	)
	server := requestServer(t, func(w nethttp.ResponseWriter, r *nethttp.Request) {
		body, _ := io.ReadAll(r.Body)
		gotMethod, gotBody, gotType, gotHost = r.Method, string(body), r.Header.Get("Content-Type"), r.Host
		w.WriteHeader(nethttp.StatusCreated)
	})
	rc := newRequestContext()

	result, err := requestRun(rc, map[string]any{
		"url":    server.URL,
		"method": "post",
		"body":   `{"version":"1.4.0"}`,
		"headers": map[string]any{
			"Content-Type": "application/json",
			"Host":         "api.example.com",
		},
		"status_code": 201,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !result.Changed {
		t.Error("a POST reported no change: what it did to the far side cannot be inspected from here")
	}

	// Upper-cased on the way out, because HTTP method names are case
	// sensitive and a server handed "post" answers 501.
	if gotMethod != "POST" {
		t.Errorf("the server saw method %q, want POST", gotMethod)
	}
	if gotBody != `{"version":"1.4.0"}` {
		t.Errorf("the server saw body %q, want it sent exactly as written", gotBody)
	}
	if gotType != "application/json" {
		t.Errorf("the server saw Content-Type %q, want application/json", gotType)
	}
	if gotHost != "api.example.com" {
		t.Errorf("the server saw Host %q, want the header to have become the request's real host", gotHost)
	}
}

// TestRequest_ChangedFollowsTheVerb pins the safe-method list against
// RFC 9110 rather than against intuition.
//
// OPTIONS and TRACE are the two most likely to be got wrong, since
// neither is a verb people think about, and both are read-only by
// definition.
func TestRequest_ChangedFollowsTheVerb(t *testing.T) {
	server := requestServer(t, requestOK)

	tests := []struct {
		method  string
		changed bool
	}{
		{method: "GET", changed: false},
		{method: "HEAD", changed: false},
		{method: "OPTIONS", changed: false},
		{method: "TRACE", changed: false},
		{method: "POST", changed: true},
		{method: "PUT", changed: true},
		{method: "PATCH", changed: true},
		{method: "DELETE", changed: true},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			result, err := requestRun(newRequestContext(), map[string]any{
				"url":    server.URL,
				"method": tt.method,
			})
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			if result.Changed != tt.changed {
				t.Errorf("Changed = %v, want %v for %s", result.Changed, tt.changed, tt.method)
			}
		})
	}
}

// TestRequest_AcceptsTheStatusCodesItWasGiven covers the three shapes a
// status_code arrives in, each against a server answering something that
// is NOT the 200 default.
//
// Asserting against 200 anywhere here would pass just as happily against
// a method that ignored the parameter entirely.
func TestRequest_AcceptsTheStatusCodesItWasGiven(t *testing.T) {
	tests := []struct {
		name    string
		answers int
		codes   any
	}{
		{name: "one number", answers: nethttp.StatusNoContent, codes: 204},
		{name: "a list", answers: nethttp.StatusAccepted, codes: []any{201, 202}},
		{name: "a number that crossed a JSON boundary", answers: nethttp.StatusCreated, codes: float64(201)},
		{name: "a number that arrived as an int64", answers: nethttp.StatusCreated, codes: int64(201)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answers := tt.answers
			server := requestServer(t, func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.WriteHeader(answers)
			})
			rc := newRequestContext()

			if _, err := requestRun(rc, map[string]any{
				"url":         server.URL,
				"status_code": tt.codes,
			}); err != nil {
				t.Fatalf("Request: %v", err)
			}
			if got := rc.stats["status"]; got != answers {
				t.Errorf("status stat = %v, want %d", got, answers)
			}
		})
	}
}

// TestRequest_UnexpectedStatusFailsAfterRecording proves the failure
// keeps the evidence.
//
// The body of a 4xx is usually the only thing that says why, so a method
// that returned the error without recording first would leave an operator
// with a number and nothing else.
func TestRequest_UnexpectedStatusFailsAfterRecording(t *testing.T) {
	server := requestServer(t, func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNotFound)
		_, _ = io.WriteString(w, "no such release")
	})
	rc := newRequestContext()

	_, err := requestRun(rc, map[string]any{"url": server.URL})
	if err == nil {
		t.Fatal("a 404 was reported as success")
	}
	if !strings.Contains(err.Error(), "returned 404") {
		t.Errorf("error = %q, want it to name the status that arrived", err)
	}
	if !strings.Contains(err.Error(), "wanted 200") {
		t.Errorf("error = %q, want it to name the status that was expected", err)
	}
	if got := rc.stats["content"]; got != "no such release" {
		t.Errorf("content stat = %v, want the body that explains the failure to have been recorded", got)
	}
	if got := rc.stats["status"]; got != 404 {
		t.Errorf("status stat = %v, want 404 recorded even though the task failed", got)
	}
}

// TestRequest_UnexpectedStatusNamesEveryAcceptedCode proves the failure
// message lists the whole accepted set, not just the first of it.
func TestRequest_UnexpectedStatusNamesEveryAcceptedCode(t *testing.T) {
	server := requestServer(t, requestOK)

	_, err := requestRun(newRequestContext(), map[string]any{
		"url":         server.URL,
		"status_code": []any{201, 202},
	})
	if err == nil {
		t.Fatal("a 200 was accepted by a task that asked for 201 or 202")
	}
	if !strings.Contains(err.Error(), "wanted one of 201, 202") {
		t.Errorf("error = %q, want it to list every accepted status", err)
	}
}

// TestRequest_VerifiesCertificatesByDefault is the security property this
// method cannot be allowed to lose.
//
// httptest's TLS server presents a certificate no trust store knows, so a
// task that did not ask to skip verification MUST fail. Nothing may be
// recorded either: a run that never got an answer has no response to
// report, and a status stat left over from somewhere would be read as one.
func TestRequest_VerifiesCertificatesByDefault(t *testing.T) {
	server := requestTLSServer(t, requestOK)
	rc := newRequestContext()

	_, err := requestRun(rc, map[string]any{"url": server.URL})
	if err == nil {
		t.Fatal("an unknown certificate was accepted, so verification is off by default")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Errorf("error = %q, want it to say the certificate was the problem", err)
	}
	if len(rc.stats) != 0 {
		t.Errorf("stats = %v, want none for a request that got no response", rc.stats)
	}
}

// TestRequest_SkipsVerificationOnlyWhenAsked proves the escape hatch
// works, against the same server the test above proves is rejected.
//
// The pair is the point: one of them alone would pass against a method
// that always verified, or against one that never did.
func TestRequest_SkipsVerificationOnlyWhenAsked(t *testing.T) {
	server := requestTLSServer(t, requestOK)
	rc := newRequestContext()

	if _, err := requestRun(rc, map[string]any{
		"url":            server.URL,
		"validate_certs": false,
	}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if got := rc.stats["content"]; got != "hello" {
		t.Errorf("content stat = %v, want the body from the server whose certificate was skipped", got)
	}
}

// TestRequest_FollowsARedirectAndRecordsWhereItLanded proves the url stat
// is the URL the answer came from rather than the one that was asked for.
func TestRequest_FollowsARedirectAndRecordsWhereItLanded(t *testing.T) {
	server := requestServer(t, func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Path == "/moved" {
			nethttp.Redirect(w, r, "/settled", nethttp.StatusFound)
			return
		}
		requestOK(w, r)
	})
	rc := newRequestContext()

	if _, err := requestRun(rc, map[string]any{"url": server.URL + "/moved"}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if got := rc.stats["url"]; got != server.URL+"/settled" {
		t.Errorf("url stat = %v, want the URL the redirect landed on", got)
	}
}

// TestRequest_TimesOut proves the timeout is really applied and is really
// in seconds, using a fraction that no whole-second parameter could
// express.
func TestRequest_TimesOut(t *testing.T) {
	server := requestServer(t, func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		// Waits for the client to give up rather than for a fixed time, so
		// the timeout under test is the only thing deciding how long this
		// takes and the server is not still sleeping when the test ends.
		// The upper bound is there so that a method ignoring the timeout
		// entirely fails this test rather than hanging both sides of it.
		select {
		case <-r.Context().Done():
		case <-time.After(700 * time.Millisecond):
		}
	})

	started := time.Now()
	_, err := requestRun(newRequestContext(), map[string]any{
		"url":     server.URL,
		"timeout": 0.05,
	})
	if err == nil {
		t.Fatal("a request that outlasted its timeout was reported as success")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("the call took %v, want it abandoned near the 0.05s the task asked for", elapsed)
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Errorf("error = %q, want it to name the URL that timed out", err)
	}
}

// TestRequest_ConnectionFailureIsReported covers the branch where nothing
// answered at all, using an address that really has nothing listening.
func TestRequest_ConnectionFailureIsReported(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(requestOK))
	address := server.URL
	server.Close()

	rc := newRequestContext()
	_, err := requestRun(rc, map[string]any{"url": address})
	if err == nil {
		t.Fatal("a request to a closed port was reported as success")
	}
	if !strings.Contains(err.Error(), "GET "+address) {
		t.Errorf("error = %q, want it to name the request that failed", err)
	}
	if len(rc.stats) != 0 {
		t.Errorf("stats = %v, want none for a request that got no response", rc.stats)
	}
}

// TestRequest_BodyReadFailureIsReported covers the branch where headers
// arrived and the body did not finish.
//
// The server hijacks the connection and writes a Content-Length it then
// fails to satisfy, which is what a connection dropping mid-response
// looks like from the client side. Reporting the bytes that did arrive as
// the content would hand a later condition a truncated document that
// still parses, which is the worst available outcome.
func TestRequest_BodyReadFailureIsReported(t *testing.T) {
	server := httptest.NewUnstartedServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		conn, _, err := w.(nethttp.Hijacker).Hijack()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 64\r\n\r\nshort"))
		_ = conn.Close()
	}))
	// The server logs the hijacked connection's abrupt end, which is the
	// behavior under test rather than a problem to report.
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Start()
	t.Cleanup(server.Close)

	rc := newRequestContext()
	_, err := requestRun(rc, map[string]any{"url": server.URL})
	if err == nil {
		t.Fatal("a truncated body was reported as a complete response")
	}
	if !strings.Contains(err.Error(), "reading the response body") {
		t.Errorf("error = %q, want it to name the part of the exchange that failed", err)
	}
	if got, recorded := rc.stats["content"]; recorded {
		t.Errorf("content stat = %v, want nothing recorded: the body never finished arriving", got)
	}
}

// TestRequest_StatRecordFailureIsReported covers the branch where the
// call succeeded and recording its answer did not.
//
// Swallowing it would leave a task reporting success with no record of
// what came back, which is worse than a failed task.
func TestRequest_StatRecordFailureIsReported(t *testing.T) {
	server := requestServer(t, requestOK)
	rc := newRequestContext()
	rc.failOn = "status"

	_, err := requestRun(rc, map[string]any{"url": server.URL})
	if err == nil {
		t.Fatal("a failure to record the response was swallowed")
	}
	if !errors.Is(err, errRequestStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
}

// TestRequest_RefusesAURLItCannotCall covers every way the one required
// parameter can be wrong, and proves each refusal happens before anything
// is opened.
//
// The scheme cases are the ones that matter. Handing file:// to net/http
// produces an error about an unsupported protocol scheme from deep inside
// the transport, naming neither the task nor what to write instead, and a
// bare host with no scheme at all is the most common way anyone writes a
// URL wrongly.
func TestRequest_RefusesAURLItCannotCall(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{name: "absent", params: nil, want: "url is required"},
		{name: "empty", params: map[string]any{"url": ""}, want: "url is required"},
		{name: "not text", params: map[string]any{"url": 8080}, want: "url is required"},
		{name: "a file path", params: map[string]any{"url": "file:///etc/passwd"}, want: "speaks only http and https"},
		{name: "an ftp URL", params: map[string]any{"url": "ftp://example.com/x"}, want: "speaks only http and https"},
		{name: "no scheme at all", params: map[string]any{"url": "example.com/health"}, want: "speaks only http and https"},
		{name: "no host", params: map[string]any{"url": "http:///health"}, want: "names no host"},
		{name: "unreadable", params: map[string]any{"url": "http://[::1"}, want: "cannot be read as a URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := requestRun(newRequestContext(), tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestRequest_RefusesEverythingElseItCannotSend covers the remaining
// parameter refusals in one table, since each is the same kind of
// mistake: a value that arrived as something the request cannot be built
// from.
func TestRequest_RefusesEverythingElseItCannotSend(t *testing.T) {
	const url = "http://example.com/health"

	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{name: "a method HTTP cannot send", params: map[string]any{"url": url, "method": "GET IT"}, want: "invalid method"},
		{name: "headers that are not a mapping", params: map[string]any{"url": url, "headers": "Accept: text/plain"}, want: "must be a mapping"},
		{name: "a header value that is not text", params: map[string]any{"url": url, "headers": map[string]any{"X-Port": 8080}}, want: `headers["X-Port"] is int, not text`},
		{name: "a status code that is text", params: map[string]any{"url": url, "status_code": "200"}, want: "is string, not a number"},
		{name: "a status code inside a list that is text", params: map[string]any{"url": url, "status_code": []any{200, "201"}}, want: "is string, not a number"},
		{name: "an empty status code list", params: map[string]any{"url": url, "status_code": []any{}}, want: "is an empty list"},
		{name: "a status code below the range", params: map[string]any{"url": url, "status_code": 99}, want: "is not an HTTP status code"},
		{name: "a status code above the range", params: map[string]any{"url": url, "status_code": 600}, want: "is not an HTTP status code"},
		{name: "a status code with a fraction", params: map[string]any{"url": url, "status_code": 200.5}, want: "is not an HTTP status code"},
		{name: "a timeout that is text", params: map[string]any{"url": url, "timeout": "5"}, want: "not a number of seconds"},
		{name: "a timeout of zero", params: map[string]any{"url": url, "timeout": 0}, want: "is not a wait"},
		{name: "a negative timeout", params: map[string]any{"url": url, "timeout": -1.5}, want: "is not a wait"},
		{name: "validate_certs that is not a boolean", params: map[string]any{"url": url, "validate_certs": "no"}, want: "validate_certs must be true or false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := requestRun(newRequestContext(), tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}
