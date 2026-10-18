// Tests for http.check against real HTTP servers: what passes, what fails,
// and that the body is never in a result or an error.
package http_test

import (
	"context"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpcoll "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/http"
)

// checkCtx keeps every stat.
type checkCtx struct{ stats map[string]any }

func (c *checkCtx) InjectSecrets() map[string]string { return nil }
func (c *checkCtx) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *checkCtx) EmitFact(k string, v any) error   { return c.SetStat(k, v) }

const secretBody = `{"status":"ok","session":"BODY-SENTINEL-5521"}`

func run(t *testing.T, params map[string]any) (*checkCtx, error) {
	t.Helper()
	rc := &checkCtx{stats: map[string]any{}}
	_, err := httpcoll.Check(context.Background(), rc, nil, params)
	return rc, err
}

// noBody fails the test when the body is anywhere in rc's stats or err.
func noBody(t *testing.T, rc *checkCtx, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "BODY-SENTINEL") {
		t.Errorf("the error holds the body: %v", err)
	}
	if strings.Contains(fmt.Sprint(rc.stats), "BODY-SENTINEL") {
		t.Errorf("the stats hold the body: %v", rc.stats)
	}
}

// TestCheck_StatusAndBody: a matching answer passes; a body or status
// that differs fails naming its size and digest; neither shows the body.
func TestCheck_StatusAndBody(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(nethttp.StatusNotFound)
		}
		_, _ = w.Write([]byte(secretBody))
	}))
	defer srv.Close()
	rc, err := run(t, map[string]any{"url": srv.URL + "/healthz?token=abc", "body_contains": `"status":"ok"`})
	if err != nil {
		t.Fatal(err)
	}
	if rc.stats["status"] != 200 || rc.stats["matched"] != true || rc.stats["body_bytes"] != len(secretBody) || strings.Contains(fmt.Sprint(rc.stats["final_url"]), "token") {
		t.Errorf("stats %v", rc.stats)
	}
	noBody(t, rc, err)
	for name, params := range map[string]map[string]any{
		"body":   {"url": srv.URL, "body_matches": `"status":"down"`},
		"status": {"url": srv.URL + "/missing"},
	} {
		rc, err := run(t, params)
		if err == nil || !strings.Contains(err.Error(), "sha256") {
			t.Errorf("%s: %v", name, err)
		}
		noBody(t, rc, err)
	}
	if _, err := run(t, map[string]any{"url": srv.URL + "/missing", "status_code": []any{404, 410}}); err != nil {
		t.Errorf("an accepted 404: %v", err)
	}
}

// TestCheck_Redirects: hops within the bound are followed and listed;
// past it, or from https to http, the check fails.
func TestCheck_Redirects(t *testing.T) {
	plain := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) { _, _ = w.Write([]byte("ok")) }))
	defer plain.Close()
	var hop *httptest.Server
	hop = httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		n := len(r.URL.Path)
		if n < 4 {
			nethttp.Redirect(w, r, hop.URL+r.URL.Path+"x?code=secret", nethttp.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer hop.Close()
	rc, err := run(t, map[string]any{"url": hop.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if hops, _ := rc.stats["redirects"].([]any); len(hops) != 3 || strings.Contains(fmt.Sprint(hops), "secret") {
		t.Errorf("redirects %v", rc.stats["redirects"])
	}
	if _, err := run(t, map[string]any{"url": hop.URL + "/", "max_redirects": 2}); err == nil || !strings.Contains(err.Error(), "302") {
		t.Errorf("past the bound: %v", err)
	}
	// With no redirect followed, the redirect is the answer, and a check may
	// ask for it.
	if _, err := run(t, map[string]any{"url": hop.URL + "/", "max_redirects": 0, "status_code": 302}); err != nil {
		t.Errorf("a 302 asked for with max_redirects 0: %v", err)
	}
	down := httptest.NewTLSServer(nethttp.RedirectHandler(plain.URL, nethttp.StatusFound))
	defer down.Close()
	if _, err := run(t, map[string]any{"url": down.URL, "validate_certs": false}); err == nil || !strings.Contains(err.Error(), "https to http") {
		t.Errorf("a downgrade: %v", err)
	}
}

// TestCheck_Refuses: what a check may not be is refused before any
// request.
func TestCheck_Refuses(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"a credential in the URL": {"url": "https://u:p@example.invalid/"},
		"a POST":                  {"url": "https://example.invalid/", "method": "POST"},
		"HEAD with a body match":  {"url": "https://example.invalid/", "method": "HEAD", "body_contains": "x"},
		"both matchers":           {"url": "https://example.invalid/", "body_contains": "x", "body_matches": "y"},
		"too many redirects":      {"url": "https://example.invalid/", "max_redirects": 11},
		"a bad pattern":           {"url": "https://example.invalid/", "body_matches": "("},
		"another scheme":          {"url": "ftp://example.invalid/"},
		"a number to contain":     {"url": "https://example.invalid/", "body_contains": 200},
		"a number to match":       {"url": "https://example.invalid/", "body_matches": 2.0},
	} {
		if _, err := run(t, params); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// No refusal quotes the URL as given, whose user part or query can be a
	// credential: not for a mistyped scheme, nor for a URL that does not parse.
	for _, raw := range []string{"htps://admin:pw-sentinel@example.invalid/x?token=tok-sentinel", "https://admin:pw-sentinel@exa mple/?token=tok-sentinel", "https://admin:pw-sentinel@example.invalid/?token=tok-sentinel"} {
		_, err := run(t, map[string]any{"url": raw})
		if err == nil || strings.Contains(err.Error(), "pw-sentinel") || strings.Contains(err.Error(), "tok-sentinel") {
			t.Errorf("%q: %v", raw, err)
		}
	}
}
