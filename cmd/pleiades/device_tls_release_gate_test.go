// Release gate for device TLS through the real binary: an API that speaks
// only TLS 1.0 is refused by default, and with its record's explicit flags
// it is onboarded and called, both commands printing the warning.
package main_test

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestDeviceTLSReleaseGate_DeprecatedTLSOnlyWhenAllowedAndAlwaysWarned:
// onboarding refuses a TLS 1.0-only API for a device whose record allows
// nothing; for one that sets tls_min_version 1.0 and
// tls_allow_deprecated_versions, `pleiades onboard` prints the warning,
// `onboard --json` returns it, and `pleiades run` prints it again beside
// the call that reached the API.
func TestDeviceTLSReleaseGate_DeprecatedTLSOnlyWhenAllowedAndAlwaysWarned(t *testing.T) {
	caFile, cert := gateIssue(t)
	var hits atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/interfaces" {
			hits.Add(1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS10}
	srv.StartTLS()
	defer srv.Close()

	env := map[string]string{"SSL_CERT_FILE": caFile, "SSL_CERT_DIR": filepath.Dir(caFile)}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "modern", "--type", "generic_http", "--set", "base_url=" + srv.URL + "/api"},
		{"add-host", "legacy", "--type", "generic_http", "--set", "base_url=" + srv.URL + "/api",
			"--set", "tls_min_version=1.0", "--set", "tls_allow_deprecated_versions=true"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	if out, err := runPleiadesWithEnv(t, dir, env, "onboard", "modern", "--timeout", "30s"); err == nil {
		t.Fatalf("a device allowing nothing onboarded a TLS 1.0-only API:\n%s", out)
	}

	out, err := runPleiadesWithEnv(t, dir, env, "onboard", "legacy", "--timeout", "30s")
	if err != nil {
		t.Fatalf("onboard legacy: %v\n%s", err, out)
	}
	if !strings.Contains(out, "WARNING:") || !strings.Contains(out, "RFC 8996") {
		t.Errorf("onboard printed no deprecated-TLS warning:\n%s", out)
	}

	out, err = runPleiadesWithEnv(t, dir, env, "onboard", "legacy", "--json", "--timeout", "30s")
	if err != nil {
		t.Fatalf("onboard --json: %v\n%s", err, out)
	}
	var res struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("onboard --json printed %q: %v", out, err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "tls_allow_deprecated_versions") {
		t.Errorf("onboard --json warnings %q, want the one naming its flag", res.Warnings)
	}

	writeFile(t, dir, "runbooks/api.yaml", "id: api\nhosts: legacy\ntasks:\n  - name: read the interfaces\n    fqcn: http.request\n    params:\n      url: /interfaces\n")
	out, err = runPleiadesWithEnv(t, dir, env, "run", "runbooks/api.yaml")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if hits.Load() != 1 {
		t.Errorf("%d requests reached /api/interfaces, want one", hits.Load())
	}
	if !strings.Contains(out, "WARNING:") || !strings.Contains(out, "RFC 8996") {
		t.Errorf("run printed no deprecated-TLS warning:\n%s", out)
	}
}
