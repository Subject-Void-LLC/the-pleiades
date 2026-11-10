// This file holds the kubeconfig import to the mixed-version rule of
// LESSONS 422: the import uses the managed Kubernetes credential type as it
// is stored, and ensures it only when no build has installed it, so an older
// build's import never rewrites a newer build's type or deletes what
// credentials of it hold.
package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
)

// TestImportKubeconfig_LeavesANewerBuildsTypeAlone stores the token type as a
// newer build would, with an input this build does not ship and a value for
// it in another organization's credential, then imports a cluster.
func TestImportKubeconfig_LeavesANewerBuildsTypeAlone(t *testing.T) {
	f := newImportFixture(t, false)
	ctx := context.Background()

	newer, ok := managed.Type("kubernetes_bearer_token")
	if !ok {
		t.Fatal("this build ships no kubernetes_bearer_token type")
	}
	newer.Inputs.Fields = append(append([]credtype.InputField(nil), newer.Inputs.Fields...),
		credtype.InputField{ID: "newer_field", Label: "A newer build's input"})
	stored, err := f.store.EnsureManagedType(ctx, newer)
	if err != nil {
		t.Fatalf("EnsureManagedType(newer) = %v", err)
	}
	other := f.client.Organization.Create().SetName("other").SaveX(ctx)
	held, err := f.store.CreateCredential(ctx, other.ID, stored.ID, "other cluster", "",
		map[string]string{"host": "https://other.example.com:6443", "bearer_token": "other-token", "newer_field": "kept"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential(newer) = %v", err)
	}

	rec := f.post(t, map[string]any{"name": "prod", "organization": f.org,
		"kubeconfig": kubeconfigFor("https://k8s.example.com:6443", testCAPEM(t), "    token: "+importToken+"\n")},
		auth.ScopeCredentialWrite, auth.ScopeInventoryWrite)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import = %d: %s", rec.Code, rec.Body)
	}

	if got := f.client.Credential.GetX(ctx, held.ID).Inputs["newer_field"]; got != "kept" {
		t.Errorf("the import deleted another organization's value for a newer build's input: %q", got)
	}
	after, err := f.store.GetTypeByNamespace(ctx, "kubernetes_bearer_token")
	if err != nil {
		t.Fatal(err)
	}
	if _, declared := after.Inputs.Field("newer_field"); !declared {
		t.Error("the import rewrote a newer build's type to this build's schema")
	}
}

// TestImportKubeconfig_RefusesACustomTypeHoldingTheNamespace seeds a custom
// type under the managed token type's namespace, as one made before an
// upgrade or left by a failed reconcile would be, whose author made
// bearer_token non-secret. The import must not adopt it: the cluster token
// would be stored under that schema and read back in plain text.
func TestImportKubeconfig_RefusesACustomTypeHoldingTheNamespace(t *testing.T) {
	f := newImportFixture(t, false)
	ctx := context.Background()

	custom, ok := managed.Type("kubernetes_bearer_token")
	if !ok {
		t.Fatal("this build ships no kubernetes_bearer_token type")
	}
	custom = custom.Clone()
	custom.Managed = false
	for i := range custom.Inputs.Fields {
		if custom.Inputs.Fields[i].ID == credtype.KubernetesInputBearerToken {
			custom.Inputs.Fields[i].Secret = false
		}
	}
	if _, err := f.store.CreateType(ctx, f.org, custom); err != nil {
		t.Fatalf("CreateType(custom) = %v", err)
	}

	rec := f.post(t, map[string]any{"name": "prod", "organization": f.org,
		"kubeconfig": kubeconfigFor("https://k8s.example.com:6443", testCAPEM(t), "    token: "+importToken+"\n")},
		auth.ScopeCredentialWrite, auth.ScopeInventoryWrite)
	if strings.Contains(rec.Body.String(), importToken) {
		t.Fatalf("the response carries the token: %s", rec.Body)
	}
	for _, c := range f.credentials(t) {
		if c.Inputs[credtype.KubernetesInputBearerToken] == importToken {
			t.Errorf("the cluster token reads back in plain text from credential %d", c.ID)
		}
	}
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "held by a custom credential type") {
		t.Fatalf("import = %d: %s, want 409 saying a custom type holds the namespace", rec.Code, rec.Body)
	}
	if creds := f.credentials(t); len(creds) != 0 {
		t.Errorf("a refused import stored %d credentials", len(creds))
	}
	if _, err := f.repo.GetByName(ctx, "prod"); err == nil {
		t.Error("a refused import stored the cluster")
	}
}
