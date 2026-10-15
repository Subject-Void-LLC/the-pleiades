// Tests of POST /inventory/kubeconfig-imports against the real inventory
// repository and credential store, with the real envelope encryption.
package api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// importFixture is an import route over real stores.
type importFixture struct {
	repo   api.DeviceRepository
	store  credstore.Store
	org    int
	router *chi.Mux
	// client is the fixture's database, for a test that needs a second
	// organization.
	client *ent.Client
}

// failingCreate is a repository whose Create fails as a name taken
// meanwhile would, for the import's undo.
type failingCreate struct{ api.DeviceRepository }

func (failingCreate) Create(context.Context, pkginventory.InventoryItem) error {
	return inventory.ErrItemExists
}

func newImportFixture(t *testing.T, failCreate bool) *importFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))
	org := client.Organization.Create().SetName("platform").SaveX(context.Background())

	f := &importFixture{store: credstore.NewEntStore(client, render.New()), org: org.ID, client: client}
	f.repo = inventory.NewEntRepository(client, inventory.NewItemFactory())
	repo := f.repo
	if failCreate {
		repo = failingCreate{f.repo}
	}
	handler := api.NewDeviceHandler(repo, inventory.NewItemFactory(), slog.New(slog.NewTextHandler(io.Discard, nil))).WithKubeconfigImport(f.store)
	f.router = chi.NewRouter()
	f.router.Post("/inventory/kubeconfig-imports", handler.ImportKubeconfig)
	return f
}

// post imports body as an identity holding scopes.
func (f *importFixture) post(t *testing.T, body map[string]any, scopes ...auth.Scope) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/inventory/kubeconfig-imports", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(contextWithIdentity(req, &auth.Identity{Subject: "u1", Role: auth.RoleOperator, Scopes: scopes}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// credentials lists the organization's credentials, as the store returns
// them: secret inputs redacted.
func (f *importFixture) credentials(t *testing.T) []credstore.Credential {
	t.Helper()
	all, err := f.store.ListCredentials(context.Background(), f.org)
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// testCAPEM is a throwaway authority, as PEM.
func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cluster ca"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// kubeconfigFor is a one-context kubeconfig for server, with user's lines.
func kubeconfigFor(server, ca, user string) string {
	return "apiVersion: v1\nkind: Config\ncurrent-context: c\ncontexts:\n- name: c\n  context: {cluster: k, user: u, namespace: team-a}\n" +
		"clusters:\n- name: k\n  cluster:\n    server: " + server + "\n    certificate-authority-data: " + base64.StdEncoding.EncodeToString([]byte(ca)) + "\n" +
		"users:\n- name: u\n  user:\n" + user
}

const importToken = "imported-cluster-token-9f2c"

// TestImportKubeconfig_MakesTheClusterAndItsCredential: a token kubeconfig
// becomes a cluster record and an encrypted kubernetes_bearer_token
// credential, and the token appears in no response.
func TestImportKubeconfig_MakesTheClusterAndItsCredential(t *testing.T) {
	f := newImportFixture(t, false)
	ca := testCAPEM(t)
	rec := f.post(t, map[string]any{"name": "prod", "organization": f.org,
		"kubeconfig": kubeconfigFor("https://k8s.example.com:6443", ca, "    token: "+importToken+"\n")},
		auth.ScopeCredentialWrite, auth.ScopeInventoryWrite)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), importToken) {
		t.Fatal("the response carries the token")
	}
	var dto struct {
		Credential              int    `json:"credential"`
		CredentialTypeNamespace string `json:"credential_type_namespace"`
		Namespace               string `json:"namespace"`
		Identity                string `json:"identity"`
		PinnedCA                bool   `json:"pinned_ca"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.CredentialTypeNamespace != "kubernetes_bearer_token" || dto.Namespace != "team-a" || dto.Identity != "token" || !dto.PinnedCA {
		t.Errorf("reported %+v", dto)
	}

	device, err := f.repo.GetByName(context.Background(), "prod")
	if err != nil {
		t.Fatal(err)
	}
	cluster, ok := device.(*kubernetes.Cluster)
	if !ok || cluster.KubeAPIServer() != "https://k8s.example.com:6443" || cluster.KubeNamespace() != "team-a" || string(cluster.TLSSettings().CAPEM()) != ca {
		t.Errorf("the stored record is %#v", device)
	}
	if device.State() != pkginventory.StateDiscovered {
		t.Errorf("an imported cluster is %s, want discovered until onboarded", device.State())
	}

	creds := f.credentials(t)
	if len(creds) != 1 || creds[0].ID != dto.Credential || creds[0].Name != "prod (kubeconfig)" {
		t.Fatalf("stored credentials %+v", creds)
	}
	if creds[0].Inputs["host"] != "https://k8s.example.com:6443" || creds[0].Inputs["bearer_token"] == importToken {
		t.Errorf("credential inputs %v: want the host visible and the token redacted", creds[0].Inputs)
	}
}

// TestImportKubeconfig_Refusals: each refusal leaves nothing behind.
func TestImportKubeconfig_Refusals(t *testing.T) {
	f := newImportFixture(t, false)
	ca := testCAPEM(t)
	token := kubeconfigFor("https://k8s.example.com", ca, "    token: "+importToken+"\n")
	both := []auth.Scope{auth.ScopeCredentialWrite, auth.ScopeInventoryWrite}
	for _, tc := range []struct {
		name   string
		body   map[string]any
		scopes []auth.Scope
		status int
		want   string
	}{
		{"without inventory:write", map[string]any{"name": "a", "organization": f.org, "kubeconfig": token}, []auth.Scope{auth.ScopeCredentialWrite}, http.StatusForbidden, "inventory:write"},
		{"no organization", map[string]any{"name": "a", "kubeconfig": token}, both, http.StatusBadRequest, "organization"},
		{"no name", map[string]any{"organization": f.org, "kubeconfig": token}, both, http.StatusBadRequest, "name"},
		{"an exec kubeconfig", map[string]any{"name": "a", "organization": f.org,
			"kubeconfig": kubeconfigFor("https://k8s.example.com", ca, "    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: evil\n")}, both, http.StatusUnprocessableEntity, "exec"},
		{"plain http", map[string]any{"name": "a", "organization": f.org,
			"kubeconfig": kubeconfigFor("http://k8s.example.com", ca, "    token: t\n")}, both, http.StatusUnprocessableEntity, "https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.post(t, tc.body, tc.scopes...)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("import = %d %s, want %d naming %q", rec.Code, rec.Body, tc.status, tc.want)
			}
		})
	}
	if creds := f.credentials(t); len(creds) != 0 {
		t.Errorf("refused imports left credentials: %+v", creds)
	}
	if _, err := f.repo.GetByName(context.Background(), "a"); err == nil {
		t.Error("a refused import left a device")
	}
}

// TestImportKubeconfig_Replace: an import over an existing cluster is a
// conflict unless replace is set; a replacement keeps the device's
// identity and, when the identity changes kind, replaces the credential.
func TestImportKubeconfig_Replace(t *testing.T) {
	f := newImportFixture(t, false)
	ca := testCAPEM(t)
	both := []auth.Scope{auth.ScopeCredentialWrite, auth.ScopeInventoryWrite}
	first := f.post(t, map[string]any{"name": "prod", "organization": f.org,
		"kubeconfig": kubeconfigFor("https://k8s.example.com", ca, "    token: "+importToken+"\n")}, both...)
	if first.Code != http.StatusCreated {
		t.Fatalf("first import = %d: %s", first.Code, first.Body)
	}
	before, err := f.repo.GetByName(context.Background(), "prod")
	if err != nil {
		t.Fatal(err)
	}
	// A property the operator set, which a replacement keeps.
	if err := before.(*kubernetes.Cluster).AddInfo(kubernetes.NodeEngineHostProperty, "engine", true); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Save(context.Background(), before); err != nil {
		t.Fatal(err)
	}

	certConfig := kubeconfigFor("https://k8s-new.example.com", ca,
		"    client-certificate-data: "+base64.StdEncoding.EncodeToString([]byte(ca))+"\n    client-key-data: "+base64.StdEncoding.EncodeToString([]byte("KEY-PEM"))+"\n")
	if rec := f.post(t, map[string]any{"name": "prod", "organization": f.org, "kubeconfig": certConfig}, both...); rec.Code != http.StatusConflict {
		t.Fatalf("a second import without replace = %d: %s", rec.Code, rec.Body)
	}
	rec := f.post(t, map[string]any{"name": "prod", "organization": f.org, "kubeconfig": certConfig, "replace": true}, both...)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"replaced":true`) {
		t.Fatalf("a replacement = %d: %s", rec.Code, rec.Body)
	}
	after, err := f.repo.GetByName(context.Background(), "prod")
	if err != nil {
		t.Fatal(err)
	}
	if after.ID() != before.ID() || after.(*kubernetes.Cluster).KubeAPIServer() != "https://k8s-new.example.com" {
		t.Errorf("replaced device %s at %s, want %s at the new server", after.ID(), after.(*kubernetes.Cluster).KubeAPIServer(), before.ID())
	}
	if host, _ := after.Properties().String(kubernetes.NodeEngineHostProperty); host != "engine" {
		t.Errorf("the replacement dropped node_engine_host (%q), which the import does not own", host)
	}
	if ns, _ := after.Properties().String(kubernetes.NamespaceProperty); ns != "team-a" {
		t.Errorf("namespace %q after replacement", ns)
	}
	creds := f.credentials(t)
	if len(creds) != 1 || creds[0].TypeNamespace != "pleiades_kubernetes_client_certificate" {
		t.Fatalf("credentials after replacing a token with a certificate: %+v", creds)
	}

	// A device of another type is never replaced by an import.
	if err := f.repo.Create(context.Background(), mustBuild(t, "web1", "linux_server", map[string]pkginventory.PropertyValue{"host": "10.0.0.1"})); err != nil {
		t.Fatal(err)
	}
	if rec := f.post(t, map[string]any{"name": "web1", "organization": f.org, "kubeconfig": certConfig, "replace": true}, both...); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not a kubernetes_cluster") {
		t.Errorf("replacing a server = %d: %s", rec.Code, rec.Body)
	}
}

// TestImportKubeconfig_ARefusedDeviceRemovesItsCredential: a device write
// that fails after the credential was stored takes the credential back out.
func TestImportKubeconfig_ARefusedDeviceRemovesItsCredential(t *testing.T) {
	f := newImportFixture(t, true)
	rec := f.post(t, map[string]any{"name": "prod", "organization": f.org,
		"kubeconfig": kubeconfigFor("https://k8s.example.com", testCAPEM(t), "    token: "+importToken+"\n")},
		auth.ScopeCredentialWrite, auth.ScopeInventoryWrite)
	if rec.Code != http.StatusConflict {
		t.Fatalf("import over a failing create = %d: %s", rec.Code, rec.Body)
	}
	if creds := f.credentials(t); len(creds) != 0 {
		t.Errorf("the credential outlived its device's refusal: %+v", creds)
	}
}

// mustBuild builds a device of typ through the factory every store uses.
func mustBuild(t *testing.T, name, typ string, props map[string]pkginventory.PropertyValue) pkginventory.InventoryItem {
	t.Helper()
	item, err := inventory.NewItemFactory().Build(record.Record{ID: pkginventory.DeviceID(name), Name: name, Type: typ, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}
