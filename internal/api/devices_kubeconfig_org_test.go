// Tests that a kubeconfig import puts the cluster in the organization its
// credential is stored in, and never moves one out of another.
package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// deviceOrg is the organization the fixture's store says device name is in.
func (f *importFixture) deviceOrg(t *testing.T, name string) int {
	t.Helper()
	org, err := f.repo.(inventory.OrganizationReader).DeviceOrganization(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return org
}

// TestImportKubeconfig_PutsTheClusterInItsCredentialsOrganization: onboarding
// lends a stored credential only to a cluster of the credential's own
// organization, so the import, which stores the cluster's credential in the
// request's organization, puts the cluster there too, on a create and on a
// replace of a cluster in no organization.
func TestImportKubeconfig_PutsTheClusterInItsCredentialsOrganization(t *testing.T) {
	f := newImportFixture(t, false)
	ca := testCAPEM(t)
	body := map[string]any{"name": "prod", "organization": f.org,
		"kubeconfig": kubeconfigFor("https://k8s.example.com:6443", ca, "    token: "+importToken+"\n")}
	if rec := f.post(t, body, auth.ScopeCredentialWrite, auth.ScopeInventoryWrite); rec.Code != http.StatusCreated {
		t.Fatalf("import = %d: %s", rec.Code, rec.Body)
	}
	if got := f.deviceOrg(t, "prod"); got != f.org {
		t.Errorf("the imported cluster is in organization %d, want %d", got, f.org)
	}
}

// TestImportKubeconfig_AReplaceNeverMovesAClusterBetweenOrganizations: a
// cluster in one organization is not replaced by an import into another,
// and nothing is written.
func TestImportKubeconfig_AReplaceNeverMovesAClusterBetweenOrganizations(t *testing.T) {
	f := newImportFixture(t, false)
	ca := testCAPEM(t)
	first := map[string]any{"name": "prod", "organization": f.org,
		"kubeconfig": kubeconfigFor("https://k8s.example.com:6443", ca, "    token: "+importToken+"\n")}
	if rec := f.post(t, first, auth.ScopeCredentialWrite, auth.ScopeInventoryWrite); rec.Code != http.StatusCreated {
		t.Fatalf("import = %d: %s", rec.Code, rec.Body)
	}
	other := f.client.Organization.Create().SetName("other").SaveX(context.Background())
	again := map[string]any{"name": "prod", "organization": other.ID, "replace": true,
		"kubeconfig": kubeconfigFor("https://elsewhere.example.com:6443", ca, "    token: other-token-0000\n")}
	rec := f.post(t, again, auth.ScopeCredentialWrite, auth.ScopeInventoryWrite)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "another organization") {
		t.Fatalf("a replace into another organization = %d: %s", rec.Code, rec.Body)
	}
	if got := f.deviceOrg(t, "prod"); got != f.org {
		t.Errorf("the cluster moved to organization %d", got)
	}
	others, err := f.store.ListCredentials(context.Background(), other.ID)
	if err != nil || len(others) != 0 {
		t.Errorf("the refused replace stored credentials %v, %v", others, err)
	}
}
