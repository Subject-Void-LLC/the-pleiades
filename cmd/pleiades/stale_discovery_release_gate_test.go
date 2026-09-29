// Release gate for Phase 117a's S2: through the real binary, a
// generic_http device repointed after onboarding sends its stored
// credential nowhere new until it is onboarded again, however the record
// was changed.
package main_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// staleGateToken is the device's stored bearer token.
const staleGateToken = "stale-token-8Wc3nZ2q"

// staleGateServer starts a TLS API that answers the onboarding probe and
// /status for staleGateToken, and counts every request that carried it.
func staleGateServer(t *testing.T, cert tls.Certificate, carried *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+staleGateToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		carried.Add(1)
		switch r.URL.Path {
		case "/api/openapi.json":
			_, _ = w.Write([]byte(`{"openapi":"3.0.3","info":{"title":"Stale API","version":"1"},"paths":{"/status":{}}}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// TestRepointedHTTPDeviceSendsItsCredentialNowhereNew is S2 of Phase 117a.
// Before the fix, base_url was an ordinary property: changing it needed
// only inventory:write, and the next path call sent the device's stored
// credential to the new host. Here the "attacker" server is a second API
// the same CA vouches for, so TLS verification alone would admit it; only
// the discovery's binding stands between the credential and that host.
// Both ways of changing the record are driven: set-host, and a hand edit of
// inventory.yaml, which no write path sees.
func TestRepointedHTTPDeviceSendsItsCredentialNowhereNew(t *testing.T) {
	caFile, cert := gateIssue(t)
	var legitimate, attacker atomic.Int32
	good := staleGateServer(t, cert, &legitimate)
	evil := staleGateServer(t, cert, &attacker)

	env := map[string]string{"SSL_CERT_FILE": caFile, "SSL_CERT_DIR": filepath.Dir(caFile)}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "api1", "--type", "generic_http", "--set", "base_url=" + good.URL + "/api", "--set", "http_auth=bearer", "--set", "openapi_path=/openapi.json"},
		{"add-credential", "api1", "--username", "token", "--password", staleGateToken},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	onboardJSON(t, dir, env, "api1")
	writeFile(t, dir, "runbooks/status.yaml", "id: status\nhosts: api1\ntasks:\n  - name: read the status\n    http.request:\n      url: /status\n")
	if out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/status.yaml"); err != nil {
		t.Fatalf("the control run on the onboarded device failed: %v\n%s", err, out)
	}

	refused := func(how string) {
		t.Helper()
		out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/status.yaml")
		if err == nil {
			t.Fatalf("%s: the run on a repointed device succeeded:\n%s", how, out)
		}
		if !strings.Contains(out, "pleiades onboard api1") {
			t.Errorf("%s: the refusal does not say to onboard again:\n%s", how, out)
		}
		if attacker.Load() != 0 {
			t.Fatalf("%s: the repointed device sent its credential to the new host %d times", how, attacker.Load())
		}
	}

	// Through a write path.
	if out, err := runPleiadesWithEnv(t, dir, env, "set-host", "api1", "--set", "base_url="+evil.URL+"/api"); err != nil {
		t.Fatalf("set-host: %v\n%s", err, out)
	}
	refused("after set-host")

	// Back to the onboarded address, the discovery holds again: the rule
	// is the binding, not a flag some write cleared.
	if out, err := runPleiadesWithEnv(t, dir, env, "set-host", "api1", "--set", "base_url="+good.URL+"/api"); err != nil {
		t.Fatalf("set-host back: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/status.yaml"); err != nil {
		t.Fatalf("the run back at the onboarded address failed: %v\n%s", err, out)
	}

	// Around every write path: a hand edit of the inventory file.
	inv := filepath.Join(dir, "inventory.yaml")
	raw, err := os.ReadFile(inv)
	if err != nil {
		t.Fatalf("reading the inventory: %v", err)
	}
	edited := strings.ReplaceAll(string(raw), good.URL, evil.URL)
	if edited == string(raw) {
		t.Fatalf("the inventory does not hold the base URL %s to edit:\n%s", good.URL, raw)
	}
	if err := os.WriteFile(inv, []byte(edited), 0o600); err != nil {
		t.Fatalf("editing the inventory: %v", err)
	}
	refused("after a hand edit")

	// Onboarding again is the deliberate act that moves the credential.
	onboardJSON(t, dir, env, "api1")
	if out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/status.yaml"); err != nil {
		t.Fatalf("the run after onboarding the new address failed: %v\n%s", err, out)
	}
	if attacker.Load() == 0 {
		t.Error("after onboarding the new address, no request reached it, so the refusals above prove nothing")
	}
}
