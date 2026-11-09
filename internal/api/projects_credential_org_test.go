// This file holds a project to the credentials of its own organization,
// through the real router, the real ent-backed project store and the real
// credential store, whose redacted projection is what says which
// organization owns a credential.
//
// The credential check searched every organization's credentials and never
// compared the credential's organization with the project's, so a project in
// one organization could bind another's source control credential, and a
// sync would then send it to a host the project names.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// realCredentialFixture is a project fixture whose router reads credentials
// through the real credential store, and two source control credentials in
// it: one the fixture's organization owns and one another organization owns.
type realCredentialFixture struct {
	*projectFixture
	own, foreign int
}

// newRealCredentialFixture builds the fixture over the project fixture's own
// database, with the credential encryption installed as the controller
// installs it, and a second router whose credential lister is the real store.
func newRealCredentialFixture(t *testing.T) *realCredentialFixture {
	t.Helper()
	f := newProjectFixture(t)
	ctx := context.Background()

	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService: %v", err)
	}
	f.client.Credential.Use(crypto.CredentialInputsHook(svc))
	f.client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))

	creds := credstore.NewEntStore(f.client, render.New())
	if err := credstore.ReconcileManaged(ctx, creds, managed.Types(), nil); err != nil {
		t.Fatalf("ReconcileManaged: %v", err)
	}
	scm, err := creds.GetTypeByNamespace(ctx, "scm")
	if err != nil {
		t.Fatalf("reading the Source Control type: %v", err)
	}
	own, err := creds.CreateCredential(ctx, f.orgID, scm.ID, "own-forge", "",
		map[string]string{"username": "git", "password": "ghp-own-secret"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential(own): %v", err)
	}
	globex := f.client.Organization.Create().SetName("globex").SaveX(ctx)
	foreign, err := creds.CreateCredential(ctx, globex.ID, scm.ID, "globex-forge", "",
		map[string]string{"username": "git", "password": "ghp-globex-secret"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential(globex): %v", err)
	}

	handler := api.NewProjectHandler(f.store, f.runner, creds, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.GetProject.Route(handler.Get),
			apispec.CreateProject.Route(handler.Create),
			apispec.UpdateProject.Route(handler.Update),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	f.router = router
	return &realCredentialFixture{projectFixture: f, own: own.ID, foreign: foreign.ID}
}

// assertCrossTenantRefusal checks a write naming another organization's
// credential was refused on the credential field, and that the refusal names
// neither that organization nor anything of the credential but its field.
func assertCrossTenantRefusal(t *testing.T, what string, status int, resp []byte) {
	t.Helper()
	if status != http.StatusBadRequest || !strings.Contains(string(resp), "credential") {
		t.Errorf("%s: status %d, want 400 naming the credential field; body %s", what, status, resp)
	}
	for _, leak := range []string{"globex", "forge", "secret"} {
		if strings.Contains(string(resp), leak) {
			t.Errorf("%s: the refusal names %q: %s", what, leak, resp)
		}
	}
}

// TestProjects_BindOnlyACredentialOfTheirOwnOrganization refuses another
// organization's credential on create and on PATCH, and still binds one of
// the project's own.
func TestProjects_BindOnlyACredentialOfTheirOwnOrganization(t *testing.T) {
	f := newRealCredentialFixture(t)

	status, resp := f.do(t, http.MethodPost, "/api/v1/projects", json.RawMessage(fmt.Sprintf(
		`{"name":"borrowing","organization":%d,"scm_url":"https://example.invalid/a.git","credential":%d}`, f.orgID, f.foreign)))
	assertCrossTenantRefusal(t, "creating a project with another organization's credential", status, resp)

	id := f.create(t, fmt.Sprintf(`{"name":"automation","organization":%d,"scm_url":"https://example.invalid/a.git","credential":%d}`,
		f.orgID, f.own))

	status, resp = f.do(t, http.MethodPatch, projectPath(id), json.RawMessage(fmt.Sprintf(`{"credential":%d}`, f.foreign)))
	assertCrossTenantRefusal(t, "rebinding a project to another organization's credential", status, resp)
	assertProjectFields(t, f.readProject(t, id), map[string]any{"credential": float64(f.own)})
}
