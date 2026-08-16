package main_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
	"github.com/golang-jwt/jwt/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// This file is Phase 8's own real, end-to-end proof (RULE 0) that the
// Federated Identity (JWKS) path this phase added actually works against
// the real built pleiades-controller binary, not just internal/auth's own
// package tests: the real binary is started with JWKS_URL pointed at a
// real httptest.Server, and real HTTP requests carry real RS256-signed
// tokens against its real, mounted, auth-protected route.

func rsaJWKSDocument(t *testing.T, pub *rsa.PublicKey, kid string) string {
	t.Helper()
	doc := map[string]interface{}{
		"keys": []map[string]string{
			{
				"kty": "RSA",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshaling JWKS document: %v", err)
	}
	return string(b)
}

func signRS256Token(t *testing.T, key *rsa.PrivateKey, kid, issuer, audience string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":    "e2e-user",
		"role":   "admin",
		"scopes": []string{},
		"iss":    issuer,
		"aud":    audience,
		"exp":    time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return signed
}

func waitForHealthz(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("controller did not become healthy within 15s")
}

// trustControllerCertificate waits for the certificate the controller
// provisions for itself and returns a client that verifies against exactly
// that one.
//
// WHY THIS EXISTS RATHER THAN A PLAIN http.Client. This gate used to tell
// the controller that something upstream had terminated TLS, which made it
// serve plain HTTP for the convenience of the test. That is a real
// arrangement the product supports, but it is not the one an operator gets
// by default, and RULE 0 asks for the path the user runs. The default is
// this: no TLS configuration at all, so the binary generates a certificate,
// persists it, and serves HTTPS. Pointing the client at that file costs a
// few lines and puts the auth assertions below on the transport the
// controller really serves.
//
// InsecureSkipVerify is deliberately NOT used. The pool holds one
// certificate read from the directory the process was told to write it to,
// so a controller serving anything else fails the handshake instead of
// quietly passing.
func trustControllerCertificate(t *testing.T, dir string) *http.Client {
	t.Helper()

	certPath := filepath.Join(dir, tlscert.CertFileName)
	var pem []byte
	deadline := time.Now().Add(15 * time.Second)
	for {
		var err error
		pem, err = os.ReadFile(certPath) // #nosec G304 -- a path inside this test's own t.TempDir()
		if err == nil && len(pem) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the controller never wrote a serving certificate to %s: %v", certPath, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatalf("the certificate at %s is not usable PEM", certPath)
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}},
	}
}

func postDispatch(t *testing.T, client *http.Client, baseURL, bearerToken string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/jobs/dispatch", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("issuing request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestController_JWKS_RealServer_AcceptsValidRejectsForged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start NATS container: %v", err)
	}
	t.Cleanup(func() { _ = natsContainer.Terminate(context.Background()) })
	natsURL, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get NATS connection string: %v", err)
	}

	// The real, published signing key, and a second, unrelated key that
	// is never published anywhere: an attacker with no access to the
	// IdP's private key, the forgery this test proves is rejected.
	published, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating published RSA key: %v", err)
	}
	attacker, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating attacker RSA key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rsaJWKSDocument(t, &published.PublicKey, "e2e-kid")))
	}))
	t.Cleanup(jwksServer.Close)

	port := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "controller-jwks.db")
	certDir := filepath.Join(t.TempDir(), "tls")
	masterEncryptionKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"NATS_URL="+natsURL,
		"DB_PATH="+dbPath,
		"LISTEN_ADDR=127.0.0.1:"+strconv.Itoa(port),
		"JWKS_URL="+jwksServer.URL,
		"JWT_ISSUER=pleiades-controller",
		"JWT_AUDIENCE=pleiades-api",
		"MASTER_ENCRYPTION_KEY="+masterEncryptionKey,
		// Phase 14 ("The Dispatcher") made main() construct a
		// runbook.Source at startup and fatal() if RUNBOOK_DIR (default
		// "runbooks", relative to the process's cwd) does not exist and
		// is not readable. This subprocess's cwd is this test binary's
		// own package directory, which has no such directory, so a real,
		// empty, readable one is pointed to explicitly: this test never
		// dispatches an actual runbook, only proves the JWKS auth path,
		// so an empty directory is all main() needs to start.
		"RUNBOOK_DIR="+t.TempDir(),
		// The default TLS arrangement, which is the one an operator gets
		// with nothing configured: the controller provisions a certificate
		// for itself and serves HTTPS. Only the DIRECTORY is stated, and
		// only because the default is relative to the working directory,
		// which for this subprocess is the package directory: without it
		// the run would leave a tls/ directory inside the repository. An
		// earlier version of this gate set
		// PLEIADES_TLS_TERMINATED_UPSTREAM=1 instead, which was cheaper and
		// tested a transport no default install serves.
		"PLEIADES_TLS_AUTOCERT_DIR="+certDir,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start controller: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// https, because the controller under test terminates TLS itself here,
	// exactly as it does for an operator who configured nothing.
	baseURL := fmt.Sprintf("https://127.0.0.1:%d", port)
	client := trustControllerCertificate(t, certDir)
	waitForHealthz(t, client, baseURL)

	validToken := signRS256Token(t, published, "e2e-kid", "pleiades-controller", "pleiades-api")
	forgedToken := signRS256Token(t, attacker, "e2e-kid", "pleiades-controller", "pleiades-api")

	if status := postDispatch(t, client, baseURL, ""); status != http.StatusUnauthorized {
		t.Errorf("no Authorization header: got status %d, want %d", status, http.StatusUnauthorized)
	}
	if status := postDispatch(t, client, baseURL, forgedToken); status != http.StatusUnauthorized {
		t.Errorf("token signed by a key absent from the real JWKS document: got status %d, want %d", status, http.StatusUnauthorized)
	}
	if status := postDispatch(t, client, baseURL, validToken); status == http.StatusUnauthorized {
		t.Errorf("token signed by the real, published key: got %d, expected authentication to succeed (not 401)", status)
	}
}
