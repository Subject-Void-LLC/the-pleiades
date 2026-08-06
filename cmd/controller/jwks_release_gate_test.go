package main_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
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

func waitForHealthz(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("controller did not become healthy within 15s")
}

func postDispatch(t *testing.T, baseURL, bearerToken string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/jobs/dispatch", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := http.DefaultClient.Do(req)
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
		testcontainers.WithImage("nats:2.11"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
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
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start controller: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHealthz(t, baseURL)

	validToken := signRS256Token(t, published, "e2e-kid", "pleiades-controller", "pleiades-api")
	forgedToken := signRS256Token(t, attacker, "e2e-kid", "pleiades-controller", "pleiades-api")

	if status := postDispatch(t, baseURL, ""); status != http.StatusUnauthorized {
		t.Errorf("no Authorization header: got status %d, want %d", status, http.StatusUnauthorized)
	}
	if status := postDispatch(t, baseURL, forgedToken); status != http.StatusUnauthorized {
		t.Errorf("token signed by a key absent from the real JWKS document: got status %d, want %d", status, http.StatusUnauthorized)
	}
	if status := postDispatch(t, baseURL, validToken); status == http.StatusUnauthorized {
		t.Errorf("token signed by the real, published key: got %d, expected authentication to succeed (not 401)", status)
	}
}
