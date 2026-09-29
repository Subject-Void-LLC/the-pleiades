// Release gate for Phase 117a's M1: through the real binary, a credential a
// Collection method was handed is masked in the run's output when the far
// side echoes it back, as the SSH transport's output always was.
package main_test

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// maskGateToken is the device's stored bearer token. It is long enough for
// the literal channel to mask (redact.MinLiteralLength) and shaped like no
// pattern rule, so only the credential itself, handed to the run, can mask
// it: a bearer-shaped or JWT-shaped value would be masked by a pattern rule
// whether or not the fix is present, and the test would prove nothing.
const maskGateToken = "echo-token-5Rk9vQ7m"

// TestRunMasksTheCredentialAMethodWasHanded is M1 of Phase 117a. Before
// the fix, the Crawl tier added only register_mask'd values to the run's
// masking set, never the credential a Collection method received, so an
// API that echoed the token printed it in `pleiades run --json`. The server
// here answers only a request carrying the token, and echoes it back
// without its "Bearer" prefix.
func TestRunMasksTheCredentialAMethodWasHanded(t *testing.T) {
	caFile, cert := gateIssue(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if seen != maskGateToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/openapi.json":
			_, _ = w.Write([]byte(`{"openapi":"3.0.3","info":{"title":"Echo API","version":"1"},"paths":{"/echo":{}}}`))
		case "/api/echo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"seen":%q}`, seen)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()

	env := map[string]string{"SSL_CERT_FILE": caFile, "SSL_CERT_DIR": filepath.Dir(caFile)}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "echo1", "--type", "generic_http", "--set", "base_url=" + srv.URL + "/api", "--set", "http_auth=bearer", "--set", "openapi_path=/openapi.json"},
		{"add-credential", "echo1", "--username", "token", "--password", maskGateToken},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	onboardJSON(t, dir, env, "echo1")
	writeFile(t, dir, "runbooks/echo.yaml", "id: echo\nhosts: echo1\ntasks:\n  - name: echo the credential\n    http.request:\n      url: /echo\n")

	out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/echo.yaml", "--json")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	// The stat was printed (the key is there), so an absent token is the
	// masking at work rather than a report that left the body out.
	if !strings.Contains(out, "seen") {
		t.Fatalf("the run's report does not carry the echoed body at all, so it proves nothing:\n%s", out)
	}
	if strings.Contains(out, maskGateToken) {
		t.Errorf("the run printed the credential the method was handed:\n%s", out)
	}
}
