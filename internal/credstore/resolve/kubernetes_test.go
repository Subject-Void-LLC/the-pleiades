// Tests of the host-matched Kubernetes credential lookup against real,
// encrypted credential rows.
package resolve_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestKubernetesSecrets: the one credential whose host names the API
// server is decrypted and returned as the cluster's secrets; none is nil;
// two are refused, naming both; another kind with a host input never
// matches.
func TestKubernetesSecrets(t *testing.T) {
	resolver, store, _, orgID, _ := fixture(t)
	ctx := context.Background()
	ct, ok := managed.Type("kubernetes_bearer_token")
	if !ok {
		t.Fatal("the catalog ships no kubernetes_bearer_token")
	}
	typ, err := store.EnsureManagedType(ctx, ct)
	if err != nil {
		t.Fatal(err)
	}
	add := func(name, host, token string) {
		t.Helper()
		if _, err := store.CreateCredential(ctx, orgID, typ.ID, name, "", map[string]string{"host": host, "bearer_token": token}, nil); err != nil {
			t.Fatal(err)
		}
	}
	add("prod", "https://prod.example.com:6443", "prod-token-1234")
	add("staging", "staging.example.com", "staging-token-1234")

	got, err := resolve.KubernetesSecrets(ctx, store, resolver, "https://prod.example.com:6443")
	if err != nil || got[wire.SecretBearerToken] != "prod-token-1234" {
		t.Fatalf("prod = %v, %v", got, err)
	}
	if got, err := resolve.KubernetesSecrets(ctx, store, resolver, "https://staging.example.com"); err != nil || got[wire.SecretBearerToken] != "staging-token-1234" {
		t.Errorf("staging, its host written without a scheme = %v, %v", got, err)
	}
	if got, err := resolve.KubernetesSecrets(ctx, store, resolver, "https://other.example.com"); got != nil || err != nil {
		t.Errorf("a cluster no credential names = %v, %v", got, err)
	}

	// A registry credential has a host input too, and is not a cluster's.
	regType, err := store.EnsureManagedType(ctx, mustManaged(t, "registry"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCredential(ctx, orgID, regType.ID, "registry at prod", "", map[string]string{"host": "prod.example.com:6443", "username": "u", "password": "registry-pass"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := resolve.KubernetesSecrets(ctx, store, resolver, "https://prod.example.com:6443"); err != nil || got[wire.SecretBearerToken] != "prod-token-1234" {
		t.Errorf("prod beside a registry credential for the same host = %v, %v", got, err)
	}

	add("prod again", "prod.example.com:6443", "second-token-1234")
	_, err = resolve.KubernetesSecrets(ctx, store, resolver, "https://prod.example.com:6443")
	if err == nil || !strings.Contains(err.Error(), `"prod"`) || !strings.Contains(err.Error(), `"prod again"`) {
		t.Errorf("two credentials naming prod: %v, want both named", err)
	}
}

// mustManaged is the shipped credential type namespace.
func mustManaged(t *testing.T, namespace string) credtype.CredentialType {
	t.Helper()
	ct, ok := managed.Type(namespace)
	if !ok {
		t.Fatalf("the catalog ships no %s", namespace)
	}
	return ct
}
