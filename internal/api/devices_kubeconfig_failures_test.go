// Tests of the kubeconfig import's failure paths: a store or repository
// call that fails partway, and the inputs the route refuses before writing.
package api_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// failingCredentials is the real credential store with one call made to
// fail.
type failingCredentials struct {
	credstore.Store
	failOn string
}

var errInjected = errors.New("injected failure")

func (f failingCredentials) EnsureManagedType(ctx context.Context, ct credtype.CredentialType) (credstore.CredentialType, error) {
	if f.failOn == "type" {
		return credstore.CredentialType{}, errInjected
	}
	return f.Store.EnsureManagedType(ctx, ct)
}

func (f failingCredentials) ListCredentials(ctx context.Context, org int) ([]credstore.Credential, error) {
	if f.failOn == "list" {
		return nil, errInjected
	}
	return f.Store.ListCredentials(ctx, org)
}

func (f failingCredentials) CreateCredential(ctx context.Context, org, typeID int, name, description string, inputs, external map[string]string, opts ...credstore.CredentialOption) (credstore.Credential, error) {
	if f.failOn == "create" {
		return credstore.Credential{}, errInjected
	}
	return f.Store.CreateCredential(ctx, org, typeID, name, description, inputs, external, opts...)
}

func (f failingCredentials) UpdateCredential(ctx context.Context, id int, name, description string, inputs, external map[string]string, opts ...credstore.CredentialOption) (credstore.Credential, error) {
	if f.failOn == "update" {
		return credstore.Credential{}, errInjected
	}
	return f.Store.UpdateCredential(ctx, id, name, description, inputs, external, opts...)
}

func (f failingCredentials) DeleteCredential(ctx context.Context, id int) error {
	if f.failOn == "delete" {
		return errInjected
	}
	return f.Store.DeleteCredential(ctx, id)
}

// failingRepo is the real repository with one call made to fail.
type failingRepo struct {
	api.DeviceRepository
	failOn string
	reads  int
}

func (f *failingRepo) GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error) {
	f.reads++
	if f.failOn == "read" || (f.failOn == "read-back" && f.reads > 1) {
		return nil, errInjected
	}
	return f.DeviceRepository.GetByName(ctx, name)
}

func (f *failingRepo) Save(ctx context.Context, item pkginventory.InventoryItem) error {
	if f.failOn == "save" {
		return inventory.ErrVersionConflict
	}
	return f.DeviceRepository.Save(ctx, item)
}

// importWith routes an import through repo and creds.
func (f *importFixture) importWith(t *testing.T, repo api.DeviceRepository, creds api.KubeconfigCredentials, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	handler := api.NewDeviceHandler(repo, inventory.NewItemFactory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if creds != nil {
		handler.WithKubeconfigImport(creds)
	}
	router := chi.NewRouter()
	router.Post("/inventory/kubeconfig-imports", handler.ImportKubeconfig)
	saved := f.router
	f.router = router
	defer func() { f.router = saved }()
	return f.post(t, body, auth.ScopeCredentialWrite, auth.ScopeInventoryWrite)
}

// TestImportKubeconfig_FailuresPartway: each store or repository failure
// is reported, and a new cluster's credential does not outlive its
// record's failure.
func TestImportKubeconfig_FailuresPartway(t *testing.T) {
	f := newImportFixture(t, false)
	ca := testCAPEM(t)
	token := kubeconfigFor("https://k8s.example.com", ca, "    token: "+importToken+"\n")
	body := func(replace bool) map[string]any {
		return map[string]any{"name": "prod", "organization": f.org, "kubeconfig": token, "replace": replace}
	}

	if rec := f.importWith(t, f.repo, nil, body(false)); rec.Code != http.StatusNotImplemented {
		t.Errorf("an import with no credential store = %d", rec.Code)
	}
	for _, failOn := range []string{"type", "list", "create"} {
		if rec := f.importWith(t, f.repo, failingCredentials{Store: f.store, failOn: failOn}, body(false)); rec.Code != http.StatusConflict {
			t.Errorf("a new cluster whose credential %s fails = %d: %s", failOn, rec.Code, rec.Body)
		}
	}
	if rec := f.importWith(t, &failingRepo{DeviceRepository: f.repo, failOn: "read"}, f.store, body(false)); rec.Code != http.StatusInternalServerError {
		t.Errorf("a repository that cannot read = %d", rec.Code)
	}
	if rec := f.importWith(t, &failingRepo{DeviceRepository: f.repo, failOn: "read-back"}, f.store, body(false)); rec.Code != http.StatusInternalServerError {
		t.Errorf("a cluster that cannot be read back = %d", rec.Code)
	}
	if creds := f.credentials(t); len(creds) != 1 {
		t.Fatalf("after the refusals the organization holds %d credentials, want only the one whose cluster was written", len(creds))
	}

	// The cluster exists now; replacements fail at each step.
	if rec := f.importWith(t, &failingRepo{DeviceRepository: f.repo, failOn: "save"}, f.store, body(true)); rec.Code != http.StatusConflict {
		t.Errorf("a replacement refused by the version guard = %d", rec.Code)
	}
	for _, failOn := range []string{"list", "update"} {
		rec := f.importWith(t, f.repo, failingCredentials{Store: f.store, failOn: failOn}, body(true))
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "import it again with replace") {
			t.Errorf("a replacement whose credential %s fails = %d: %s", failOn, rec.Code, rec.Body)
		}
	}
	certConfig := kubeconfigFor("https://k8s.example.com", ca,
		"    client-certificate-data: "+base64.StdEncoding.EncodeToString([]byte(ca))+"\n    client-key-data: "+base64.StdEncoding.EncodeToString([]byte("KEY"))+"\n")
	if rec := f.importWith(t, f.repo, failingCredentials{Store: f.store, failOn: "delete"},
		map[string]any{"name": "prod", "organization": f.org, "kubeconfig": certConfig, "replace": true}); rec.Code != http.StatusConflict {
		t.Errorf("a replacement that cannot remove the old type's credential = %d", rec.Code)
	}
}

// TestImportKubeconfig_WhatTheRouteRefusesOrKeeps: a body that does not
// decode, a credential name already taken, an authority that is not a
// certificate (new and replacing), and the server name and namespace a
// kubeconfig may or may not set.
func TestImportKubeconfig_WhatTheRouteRefusesOrKeeps(t *testing.T) {
	f := newImportFixture(t, false)
	ca := testCAPEM(t)
	both := []auth.Scope{auth.ScopeCredentialWrite, auth.ScopeInventoryWrite}

	req := httptest.NewRequest(http.MethodPost, "/inventory/kubeconfig-imports", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(contextWithIdentity(req, &auth.Identity{Subject: "u1", Scopes: both}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a body that does not decode = %d", rec.Code)
	}

	garbageCA := "apiVersion: v1\nkind: Config\ncurrent-context: c\ncontexts:\n- name: c\n  context: {cluster: k, user: u}\n" +
		"clusters:\n- name: k\n  cluster:\n    server: https://k8s.example.com\n    certificate-authority-data: " + base64.StdEncoding.EncodeToString([]byte("not a certificate")) + "\n" +
		"users:\n- name: u\n  user:\n    token: t\n"
	if rec := f.post(t, map[string]any{"name": "bad", "organization": f.org, "kubeconfig": garbageCA}, both...); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("an authority that is not a certificate = %d: %s", rec.Code, rec.Body)
	}

	// No namespace in the context, and a server name: the cluster works in
	// default and checks the name.
	named := "apiVersion: v1\nkind: Config\ncurrent-context: c\ncontexts:\n- name: c\n  context: {cluster: k, user: u}\n" +
		"clusters:\n- name: k\n  cluster:\n    server: https://10.0.0.9:6443\n    tls-server-name: api.cluster.internal\n    certificate-authority-data: " + base64.StdEncoding.EncodeToString([]byte(ca)) + "\n" +
		"users:\n- name: u\n  user:\n    token: " + importToken + "\n"
	rec = f.post(t, map[string]any{"name": "named", "organization": f.org, "kubeconfig": named}, both...)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"namespace":"default"`) {
		t.Fatalf("a context with no namespace = %d: %s", rec.Code, rec.Body)
	}
	device, err := f.repo.GetByName(context.Background(), "named")
	if err != nil {
		t.Fatal(err)
	}
	if device.(*kubernetes.Cluster).TLSSettings().ServerName != "api.cluster.internal" {
		t.Error("the server name was not kept")
	}

	// A credential already holding the name a new cluster's import would
	// give its own is refused without replace, and no device is written.
	typ, err := f.store.EnsureManagedType(context.Background(), mustManagedType(t, "kubernetes_bearer_token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateCredential(context.Background(), f.org, typ.ID, "taken (kubeconfig)", "", map[string]string{"host": "k.example", "bearer_token": "other-token"}, nil); err != nil {
		t.Fatal(err)
	}
	if rec := f.post(t, map[string]any{"name": "taken", "organization": f.org, "kubeconfig": named}, both...); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("a credential name taken = %d: %s", rec.Code, rec.Body)
	}
	if _, err := f.repo.GetByName(context.Background(), "taken"); err == nil {
		t.Error("a refused import left its device")
	}

	// A replacement onto an authority that is not a certificate is refused
	// before anything is written; one that drops the namespace removes it.
	first := kubeconfigFor("https://k8s.example.com", ca, "    token: "+importToken+"\n")
	if rec := f.post(t, map[string]any{"name": "prod", "organization": f.org, "kubeconfig": first}, both...); rec.Code != http.StatusCreated {
		t.Fatalf("first import = %d: %s", rec.Code, rec.Body)
	}
	if rec := f.post(t, map[string]any{"name": "prod", "organization": f.org, "kubeconfig": strings.Replace(garbageCA, "server: https://k8s.example.com", "server: https://k8s.example.com", 1), "replace": true}, both...); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a replacement onto an authority that is not a certificate = %d: %s", rec.Code, rec.Body)
	}
	noNamespace := strings.Replace(first, ", namespace: team-a", "", 1)
	if rec := f.post(t, map[string]any{"name": "prod", "organization": f.org, "kubeconfig": noNamespace, "replace": true}, both...); rec.Code != http.StatusOK {
		t.Fatalf("a replacement with no namespace = %d: %s", rec.Code, rec.Body)
	}
	after, err := f.repo.GetByName(context.Background(), "prod")
	if err != nil {
		t.Fatal(err)
	}
	if _, set := after.Properties().Raw()[kubernetes.NamespaceProperty]; set {
		t.Error("the namespace the new kubeconfig no longer sets lingered")
	}
}

// mustManagedType is the shipped credential type namespace.
func mustManagedType(t *testing.T, namespace string) credtype.CredentialType {
	t.Helper()
	ct, ok := managed.Type(namespace)
	if !ok {
		t.Fatalf("the catalog ships no %s", namespace)
	}
	return ct
}

// retypedRepo answers every read with item, reporting typ as its type, as
// a row whose stored type no longer matches its properties would.
type retypedRepo struct {
	api.DeviceRepository
	item pkginventory.InventoryItem
	typ  string
}

type retyped struct {
	pkginventory.InventoryItem
	typ string
}

func (r retyped) DeviceType() string { return r.typ }

func (r retypedRepo) GetByName(context.Context, string) (pkginventory.InventoryItem, error) {
	return retyped{InventoryItem: r.item, typ: r.typ}, nil
}

// TestDeviceUpdate_ARecordThatNoLongerBuildsIsAnInternalError: a stored
// device whose properties its type now refuses cannot be updated, and the
// caller is told only that something internal failed.
func TestDeviceUpdate_ARecordThatNoLongerBuildsIsAnInternalError(t *testing.T) {
	f := newImportFixture(t, false)
	server := mustBuild(t, "web1", "linux_server", map[string]pkginventory.PropertyValue{"host": "10.0.0.1"})
	handler := api.NewDeviceHandler(retypedRepo{DeviceRepository: f.repo, item: server, typ: kubernetes.TypeCluster},
		inventory.NewItemFactory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := chi.NewRouter()
	router.Patch("/inventory/devices/{name}", handler.Update)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/inventory/devices/web1", strings.NewReader(`{"tags": ["a"]}`)))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "api_server") {
		t.Fatalf("an update of a record that no longer builds = %d: %s", rec.Code, rec.Body)
	}
}
