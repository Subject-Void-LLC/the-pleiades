// Package api_test: a device stored under a name the name rule now refuses
// is still changed through the API, since only a create is held to the rule
// (LESSONS 415). Every character outside printable ASCII is built from its
// code point, so this file holds none (FAILURE_PATTERNS 256, 834).
package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
)

// TestDeviceUpdate_PatchesADeviceStoredUnderANameTheRuleNowRefuses seeds a
// device whose name holds an escape sequence through the generated client,
// as a create before the rule could have, and PATCHes its tags through the
// router at its escaped address. The update is stored, and the device reads
// back by that address.
func TestDeviceUpdate_PatchesADeviceStoredUnderANameTheRuleNowRefuses(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	legacy := "sw\x1b[2J1"
	client.Device.Create().SetName(legacy).SetType("linux_server").
		SetProperties(map[string]interface{}{"host": "10.0.0.1"}).SaveX(context.Background())
	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	router := deviceRouter(t, repo)
	address := api.APIVersionPrefix + "/inventory/devices/" + url.PathEscape(legacy)

	req := httptest.NewRequest(http.MethodPatch, address, strings.NewReader(`{"tags": ["patched"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH %s = %d, want 200: %s", address, rec.Code, rec.Body)
	}
	item, err := repo.GetByName(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if tags := item.Tags(); len(tags) != 1 || tags[0] != "patched" {
		t.Errorf("the stored tags are %v after the PATCH, want [patched]", tags)
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, address, nil))
	if get.Code != http.StatusOK {
		t.Errorf("GET %s = %d, want 200: %s", address, get.Code, get.Body)
	}
}

// TestImportKubeconfig_ReplacesAClusterStoredUnderANameTheRuleNowRefuses
// seeds a cluster whose name holds a direction override, as a create before
// the rule could have, and imports a kubeconfig over it with replace set.
// The replacement is a change to a stored device, not a create, so it is
// made; an import of a new cluster under such a name is still refused.
func TestImportKubeconfig_ReplacesAClusterStoredUnderANameTheRuleNowRefuses(t *testing.T) {
	f := newImportFixture(t, false)
	legacy := "prod" + string(rune(0x202E)) + "x"
	f.client.Device.Create().SetName(legacy).SetType(kubernetes.TypeCluster).
		SetProperties(map[string]interface{}{kubernetes.APIServerProperty: "https://k8s-old.example.com"}).SaveX(context.Background())
	both := []auth.Scope{auth.ScopeCredentialWrite, auth.ScopeInventoryWrite}
	config := kubeconfigFor("https://k8s.example.com", testCAPEM(t), "    token: "+importToken+"\n")

	rec := f.post(t, map[string]any{"name": legacy, "organization": f.org, "kubeconfig": config, "replace": true}, both...)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"replaced":true`) {
		t.Fatalf("replacing the stored cluster = %d, want 200 replaced: %s", rec.Code, rec.Body)
	}
	after, err := f.repo.GetByName(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if server := after.(*kubernetes.Cluster).KubeAPIServer(); server != "https://k8s.example.com" {
		t.Errorf("the stored cluster's API server is %q after the replacement, want the import's", server)
	}

	fresh := "new" + string(rune(0x202E)) + "x"
	if rec := f.post(t, map[string]any{"name": fresh, "organization": f.org, "kubeconfig": config, "replace": true}, both...); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "U+202E at byte 3") {
		t.Errorf("importing a new cluster under %q = %d, want 400 naming the character: %s", fresh, rec.Code, rec.Body)
	}
	if _, err := f.repo.GetByName(context.Background(), fresh); err == nil {
		t.Errorf("the refused new cluster %q was stored", fresh)
	}
	if creds := f.credentials(t); len(creds) != 1 {
		t.Errorf("credentials after one replacement and one refusal: %+v, want the replacement's alone", creds)
	}
}
