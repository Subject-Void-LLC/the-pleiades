// Tests that client-go's log goes only where LogTo sends it, and that a
// session's secrets reach the masker LogTo was given.
package kubeapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/klog/v2"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
)

// TestLogTo_SendsClientGosLinesThereAndNowhereElse routes klog to a buffer,
// logs a line, and finds it there; then the default (discard) again, so a
// line logged afterwards reaches neither the buffer nor standard error.
func TestLogTo_SendsClientGosLinesThereAndNowhereElse(t *testing.T) {
	var buf bytes.Buffer
	LogTo(&buf, nil)
	klog.Info("a client-go line")
	klog.Flush()
	if !strings.Contains(buf.String(), "a client-go line") {
		t.Fatalf("the routed log holds %q", buf.String())
	}

	LogTo(io.Discard, nil)
	t.Cleanup(func() { LogTo(io.Discard, nil) })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	klog.Info("a line after the default is restored")
	klog.Flush()
	os.Stderr = saved
	_ = w.Close()
	leaked, _ := io.ReadAll(r)
	if bytes.Contains(leaked, []byte("after the default")) || strings.Contains(buf.String(), "after the default") {
		t.Errorf("a line logged with the default in place reached standard error or the old writer")
	}
}

// recordedSecrets is a SecretSet that keeps what it holds.
type recordedSecrets struct {
	mu   sync.Mutex
	held map[string]bool
}

func (r *recordedSecrets) Add(values ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if v != "" {
			r.held[v] = true
		}
	}
	return len(values)
}

func (r *recordedSecrets) Forget(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		delete(r.held, v)
	}
}

func (r *recordedSecrets) has(v string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.held[v]
}

// maskingInto routes klog nowhere and a session's secrets into a new set,
// restoring the default when the test ends.
func maskingInto(t *testing.T) *recordedSecrets {
	t.Helper()
	set := &recordedSecrets{held: map[string]bool{}}
	LogTo(io.Discard, set)
	t.Cleanup(func() { LogTo(io.Discard, nil) })
	return set
}

// TestConfig_HandsItsSecretsToTheMasker: a token and a client key reach the
// set LogTo was given as soon as a configuration holds them, so a client-go
// line quoting either is masked by value, whatever its shape.
func TestConfig_HandsItsSecretsToTheMasker(t *testing.T) {
	set := maskingInto(t)
	const token, key = "staticClusterToken0a1b2c3d4e5f", "-----BEGIN EC PRIVATE KEY-----\nclientkeybody\n-----END EC PRIVATE KEY-----\n"
	if _, err := Config(Endpoint{Server: "https://cluster.example:6443"}, Identity{Token: token}, Limits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Config(Endpoint{Server: "https://cluster.example:6443"}, Identity{ClientCertPEM: []byte("cert"), ClientKeyPEM: []byte(key)}, Limits{}); err != nil {
		t.Fatal(err)
	}
	if !set.has(token) || !set.has(key) {
		t.Fatalf("the masker holds %v; want the token and the client key", set.held)
	}
	if set.has("cert") {
		t.Error("a client certificate, which is public, was masked")
	}
}

// TestIdentityFor_HandsTheEKSKeyToTheMasker: a cluster reached with tokens
// minted from an AWS key has that key's secret and session token masked,
// though no request ever carries them.
func TestIdentityFor_HandsTheEKSKeyToTheMasker(t *testing.T) {
	set := maskingInto(t)
	device := eksDevice{clusterDevice: clusterDevice{fakeItem: fakeItem{name: "prod"}, server: "https://eks.example.com"}, eks: "prod-cluster", region: "eu-west-1"}
	secrets := map[string]string{
		awscloud.SecretAccessKeyID:     "AKIAEXAMPLEKEYID0001",
		awscloud.SecretSecretAccessKey: "awsSecretAccessKeyValue0123456789",
		awscloud.SecretSessionToken:    "awsSessionTokenValue0123456789",
	}
	if _, err := identityFor(device, secrets); err != nil {
		t.Fatal(err)
	}
	if !set.has("awsSecretAccessKeyValue0123456789") || !set.has("awsSessionTokenValue0123456789") {
		t.Fatalf("the masker holds %v; want the secret access key and the session token", set.held)
	}
}

// TestTokenCache_MasksEachMintedTokenAndForgetsTheOneItReplaces: a session
// that mints a token masks it before any request carries it, and when it
// mints the next forgets the expired one, which was its alone, so a long
// session does not grow the masker by a value each refresh.
func TestTokenCache_MasksEachMintedTokenAndForgetsTheOneItReplaces(t *testing.T) {
	set := maskingInto(t)
	minted := 0
	cache := &tokenCache{mint: func(context.Context) (string, time.Time, error) {
		minted++
		// Each token expires inside the margin, so the next call mints again.
		return "mintedClusterToken" + strings.Repeat("x", minted), time.Now().Add(mintMargin / 2), nil
	}}
	first, err := cache.current(context.Background())
	if err != nil || !set.has(first) {
		t.Fatalf("first token %q (err %v) is not masked: %v", first, err, set.held)
	}
	second, err := cache.current(context.Background())
	if err != nil || second == first {
		t.Fatalf("second token = %q, err = %v; want a fresh one", second, err)
	}
	if !set.has(second) || set.has(first) {
		t.Fatalf("the masker holds %v; want the second token alone", set.held)
	}
}

// TestMaskSecrets_WithoutAMaskerDoesNothing: a process that never routed
// client-go's log discards it, and building a session there needs no set.
func TestMaskSecrets_WithoutAMaskerDoesNothing(t *testing.T) {
	LogTo(io.Discard, nil)
	maskSecrets("a value")
	replaceMasked("old", "new")
	if _, err := Config(Endpoint{Server: "https://cluster.example:6443"}, Identity{Token: "t"}, Limits{}); err != nil {
		t.Fatal(err)
	}
}
