// Tests for the Cloudflare client against a real HTTPS server answering as
// Cloudflare's API documents.
package cfapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi/cfapitest"
)

const (
	acct  = "0123456789abcdef0123456789abcdef"
	other = "fedcba9876543210fedcba9876543210"
	tun1  = "11111111-2222-3333-4444-555555555555"
	tun2  = "66666666-7777-8888-9999-000000000000"
	app1  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	zone1 = "00000000000000000000000000000001"
	zone2 = "00000000000000000000000000000002"
	token = "cf-secret-token-value"
)

// client is a client of srv with tok, sleeping no time between retries
// but counting them.
func client(srv *cfapitest.Server, tok string, slept *[]time.Duration) *cfapi.Client {
	base, _ := url.Parse(srv.URL + "/client/v4")
	c := srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cfapi.Client{Base: base, HTTP: c, Token: tok, Sleep: func(_ context.Context, d time.Duration) error {
		if slept != nil {
			*slept = append(*slept, d)
		}
		return nil
	}}
}

func server(t *testing.T) *cfapitest.Server {
	srv := cfapitest.New(t, acct, token)
	srv.Tunnels = []map[string]any{
		{"id": tun1, "name": "prod", "status": "healthy"},
		{"id": tun2, "name": "staging", "status": "degraded"},
		{"id": "99999999-9999-9999-9999-999999999999", "name": "prod", "status": "down", "deleted_at": "2026-01-01T00:00:00Z"},
	}
	srv.Connectors[tun1] = []map[string]any{{"id": "c1", "version": "2026.9.0", "arch": "linux_amd64", "conns": []map[string]any{{"colo_name": "fra01"}, {"colo_name": "ams01"}}}}
	srv.Apps = []map[string]any{{"id": app1, "name": "Admin", "domain": "admin.example.com", "aud": "audtag", "session_duration": "24h",
		"policies": []map[string]any{{"id": "p1", "name": "staff", "decision": "allow", "precedence": 1, "include": []any{map[string]any{"email_domain": map[string]any{"domain": "example.com"}}}}}}}
	srv.AuthDomain = "example.cloudflareaccess.com"
	srv.Zones = []map[string]any{
		{"id": zone1, "name": "example.com", "status": "active", "account": map[string]any{"id": acct}},
		{"id": zone2, "name": "other.com", "status": "active", "account": map[string]any{"id": other}},
	}
	srv.Records[zone1] = []map[string]any{
		{"id": "r1", "type": "A", "name": "www.example.com", "content": "192.0.2.1", "proxied": true, "ttl": 1},
		{"id": "r2", "type": "A", "name": "www.example.com", "content": "192.0.2.2", "proxied": true, "ttl": 1},
		{"id": "r3", "type": "TXT", "name": "www.example.com", "content": "v=spf1 -all", "proxied": false, "ttl": 300},
	}
	return srv
}

// TestClient_Reads: each resource reads back, a list followed across
// pages, a deleted tunnel's name ignored.
func TestClient_Reads(t *testing.T) {
	srv := server(t)
	srv.PerPage = 1
	c := client(srv, token, nil)
	ctx := context.Background()
	if a, err := c.Account(ctx, acct); err != nil || a.Name != "Example" {
		t.Fatalf("account %+v %v", a, err)
	}
	tn, err := c.Tunnel(ctx, acct, "prod")
	if err != nil || tn.ID != tun1 || tn.Status != "healthy" {
		t.Fatalf("tunnel %+v %v", tn, err)
	}
	if byID, err := c.Tunnel(ctx, acct, tun2); err != nil || byID.Name != "staging" {
		t.Fatalf("tunnel by id %+v %v", byID, err)
	}
	conns, err := c.Connectors(ctx, acct, tun1)
	if err != nil || len(conns) != 1 || len(conns[0].Conns) != 2 {
		t.Fatalf("connectors %+v %v", conns, err)
	}
	app, err := c.App(ctx, acct, "ADMIN.example.com")
	if err != nil || app.Audience != "audtag" || len(app.Policies) != 1 || app.Policies[0].Decision != "allow" {
		t.Fatalf("app %+v %v", app, err)
	}
	if iss, err := c.Issuer(ctx, acct); err != nil || iss != "https://example.cloudflareaccess.com" {
		t.Fatalf("issuer %q %v", iss, err)
	}
	z, err := c.Zone(ctx, acct, "example.com.")
	if err != nil || z.ID != zone1 {
		t.Fatalf("zone %+v %v", z, err)
	}
	recs, err := c.Records(ctx, zone1, "WWW.example.com", "A")
	if err != nil || len(recs) != 2 {
		t.Fatalf("records %+v %v", recs, err)
	}
}

// TestClient_Refuses: what may not be read is refused, before contact
// when its form is wrong.
func TestClient_Refuses(t *testing.T) {
	srv := server(t)
	c := client(srv, token, nil)
	ctx := context.Background()
	srv.Tunnels = append(srv.Tunnels, map[string]any{"id": "12121212-1212-1212-1212-121212121212", "name": "staging", "status": "healthy"})
	if _, err := c.Tunnel(ctx, acct, "staging"); err == nil || !strings.Contains(err.Error(), "name one by its id") {
		t.Errorf("two tunnels of one name: %v", err)
	}
	if _, err := c.Tunnel(ctx, acct, "absent"); !errors.Is(err, cfapi.ErrNotFound) {
		t.Errorf("no tunnel: %v", err)
	}
	if _, err := c.Zone(ctx, acct, zone2); err == nil || !strings.Contains(err.Error(), "belongs to account") {
		t.Errorf("another account's zone: %v", err)
	}
	before := len(srv.Requests())
	for name, call := range map[string]func() error{
		"account":      func() error { _, err := c.Account(ctx, "../x"); return err },
		"tunnel":       func() error { _, err := c.Tunnel(ctx, acct, "a/b?c"); return err },
		"app":          func() error { _, err := c.App(ctx, acct, "-x.example.com"); return err },
		"zone":         func() error { _, err := c.Zone(ctx, acct, "*.example.com"); return err },
		"record type":  func() error { _, err := c.Records(ctx, zone1, "www.example.com", "ANY"); return err },
		"record name":  func() error { _, err := c.Records(ctx, zone1, "www example.com", "A"); return err },
		"connectors":   func() error { _, err := c.Connectors(ctx, acct, "prod"); return err },
		"zone records": func() error { _, err := c.Records(ctx, "zone", "www.example.com", "A"); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s was read", name)
		}
	}
	if len(srv.Requests()) != before {
		t.Errorf("a malformed name reached the API: %v", srv.Requests()[before:])
	}
}

// TestClient_Errors: a refused token names the path and never the token;
// a 403 says to grant the permission; a 429 is retried as asked and a 403
// is not; a redirect is not followed; past 50 pages a list fails.
func TestClient_Errors(t *testing.T) {
	srv := server(t)
	ctx := context.Background()
	_, err := client(srv, "wrong", nil).Account(ctx, acct)
	var apiErr *cfapi.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || strings.Contains(err.Error(), "wrong") || !strings.Contains(err.Error(), "/accounts/") {
		t.Fatalf("401: %v", err)
	}
	var slept []time.Duration
	srv.Fail["/zones"] = http.StatusTooManyRequests
	if _, err := client(srv, token, &slept).Zone(ctx, acct, "example.com"); err == nil || len(slept) != 2 {
		t.Errorf("429: %v, slept %v", err, slept)
	}
	slept = nil
	srv.Fail = map[string]int{"/accounts/" + acct + "/access": http.StatusForbidden}
	if _, err := client(srv, token, &slept).Issuer(ctx, acct); err == nil || !strings.Contains(err.Error(), "give it that permission") || len(slept) != 0 {
		t.Errorf("403: %v, slept %v", err, slept)
	}
	srv.Fail = map[string]int{}

	redirect := cfapitest.New(t, acct, token)
	redirect.Config.Handler = http.RedirectHandler("https://elsewhere.invalid/", http.StatusFound)
	if _, err := client(redirect, token, nil).Account(ctx, acct); err == nil || !strings.Contains(err.Error(), "not followed") {
		t.Errorf("redirect: %v", err)
	}

	srv.PerPage = 1
	srv.Zones = nil
	for i := range 60 {
		srv.Zones = append(srv.Zones, map[string]any{"id": "z", "name": "example.com", "account": map[string]any{"id": acct}, "n": i})
	}
	if _, err := client(srv, token, nil).Zone(ctx, acct, "example.com"); err == nil || !strings.Contains(err.Error(), "more than 50 pages") {
		t.Errorf("pages: %v", err)
	}
}
