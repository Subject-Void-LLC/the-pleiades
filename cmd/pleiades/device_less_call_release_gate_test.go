// Release gate for Phase 117a's execution context on the Crawl tier:
// through the real binary, under one hosts: tag, a call that acts on no
// device runs once and a call that acts on one runs per device.
package main_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestADeviceLessCallRunsOnceUnderHosts: before Phase 117a, every task
// took its runbook's hosts:, so a runbook managing two API devices could not
// also make one call that concerned neither (a ticket update, a
// notification) without making it once per device. Now http.request with a
// full URL needs no device and runs once, while the same method with a path
// on each device's API still runs against every device hosts: names, which
// is what keeps existing runbooks unchanged.
func TestADeviceLessCallRunsOnceUnderHosts(t *testing.T) {
	caFile, cert := gateIssue(t)
	const token = "hosts-gate-token-4Jd8"
	var pings, statusA, statusB atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			// The device-less call carries no credential, and needs none.
			if r.Header.Get("Authorization") != "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			pings.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/a/openapi.json", "/b/openapi.json":
			_, _ = w.Write([]byte(`{"openapi":"3.0.3","info":{"title":"Hosts API","version":"1"},"paths":{"/status":{}}}`))
		case "/a/status":
			statusA.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/b/status":
			statusB.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()

	env := map[string]string{"SSL_CERT_FILE": caFile, "SSL_CERT_DIR": filepath.Dir(caFile)}
	dir := t.TempDir()
	steps := [][]string{{"init"}}
	for _, name := range []string{"a", "b"} {
		steps = append(steps,
			[]string{"add-host", "api-" + name, "--type", "generic_http", "--tags", "apis", "--set", "base_url=" + srv.URL + "/" + name, "--set", "http_auth=bearer", "--set", "openapi_path=/openapi.json"},
			[]string{"add-credential", "api-" + name, "--username", "token", "--password", token})
	}
	for _, args := range steps {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	onboardJSON(t, dir, env, "api-a")
	onboardJSON(t, dir, env, "api-b")
	writeFile(t, dir, "runbooks/mixed.yaml", "id: mixed\nhosts: apis\ntasks:\n"+
		"  - name: tell the ticket system once\n    http.request:\n      url: "+srv.URL+"/ping\n"+
		"  - name: read each API's status\n    http.request:\n      url: /status\n")

	out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/mixed.yaml")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if pings.Load() != 1 {
		t.Errorf("the device-less call reached the server %d times, want once", pings.Load())
	}
	if statusA.Load() != 1 || statusB.Load() != 1 {
		t.Errorf("the per-device call reached api-a %d and api-b %d times, want once each", statusA.Load(), statusB.Load())
	}
	if strings.Contains(out, token) {
		t.Error("the run printed the devices' credential")
	}
}
