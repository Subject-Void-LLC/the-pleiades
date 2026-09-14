package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// This file covers the credential surface over real HTTP, against a real
// ent-backed store with the real encryption hooks registered.
//
// Its central assertion is a disclosure check rather than a behavior one,
// and it is made against the RAW RESPONSE BYTES rather than against a
// decoded struct. Decoding into a type with no field for a secret would
// pass whether or not the secret was on the wire, which is precisely the
// bug worth catching: the question is not "does my DTO have a password
// field" but "is the password anywhere in what we sent".

// credentialFixtureSeq keeps each fixture's database distinct.
var credentialFixtureSeq atomic.Int64

// theSecret is the value these tests hunt for in raw bytes. Distinctive
// enough that finding it is a real result.
const theSecret = "sk-live-CANARY-api-9f8e7d6c5b4a"

// credentialFixture is one controller's worth of the credential surface.
type credentialFixture struct {
	client *ent.Client
	store  credstore.Store
	router http.Handler

	orgA, orgB int
	typeID     int
}

func newCredentialFixture(t *testing.T) *credentialFixture {
	t.Helper()

	client := newSerializedSQLiteClient(t, fmt.Sprintf("credentials-%d", credentialFixtureSeq.Add(1)))
	ctx := context.Background()

	// The real hooks, wired the way the composition root wires them. A
	// fixture without them would prove the redaction and nothing about the
	// encryption underneath it.
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService: %v", err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))

	orgA := client.Organization.Create().SetName("network").SaveX(ctx)
	orgB := client.Organization.Create().SetName("servers").SaveX(ctx)

	store := credstore.NewEntStore(client, render.New())
	handler := api.NewCredentialHandler(store, render.New())

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListCredentialTypes.Route(handler.ListCredentialTypes),
			apispec.GetCredentialType.Route(handler.GetCredentialType),
			apispec.CreateCredentialType.Route(handler.CreateCredentialType),
			apispec.UpdateCredentialType.Route(handler.UpdateCredentialType),
			apispec.DeleteCredentialType.Route(handler.DeleteCredentialType),
			apispec.TestCredentialType.Route(handler.TestCredentialType),
			apispec.SetCredentialTypeInputs.Route(handler.SetCredentialTypeInputs),
			apispec.ListCredentials.Route(handler.ListCredentials),
			apispec.GetCredential.Route(handler.GetCredential),
			apispec.CreateCredential.Route(handler.CreateCredential),
			apispec.UpdateCredential.Route(handler.UpdateCredential),
			apispec.DeleteCredentialEndpoint.Route(handler.DeleteCredential),
			apispec.ListTemplateCredentials.Route(handler.ListTemplateCredentials),
			apispec.SetTemplateCredentials.Route(handler.SetTemplateCredentials),
			apispec.ListCredentialInputSources.Route(handler.ListCredentialInputSources),
			apispec.SetCredentialInputSources.Route(handler.SetCredentialInputSources),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	f := &credentialFixture{client: client, store: store, router: router, orgA: orgA.ID, orgB: orgB.ID}
	f.typeID = f.createType(t, orgA.ID)
	return f
}

// do issues a request and returns the status and the RAW body.
func (f *credentialFixture) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshalling request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	return rec.Code, rec.Body.Bytes()
}

// createType creates the fixture's credential type and returns its id.
func (f *credentialFixture) createType(t *testing.T, orgID int) int {
	t.Helper()

	status, body := f.do(t, http.MethodPost, "/api/v1/credential-types", map[string]any{
		"name":         fmt.Sprintf("Custom API %d", orgID),
		"kind":         "cloud",
		"namespace":    fmt.Sprintf("custom_api_%d", orgID),
		"organization": orgID,
		"inputs": map[string]any{
			"fields": []map[string]any{
				{"id": "api_token", "label": "Token", "secret": true},
				{"id": "api_url", "label": "URL"},
			},
			"required": []string{"api_token"},
		},
		"injectors": map[string]any{
			"env": map[string]string{"API_TOKEN": "{{ api_token }}"},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the credential type: %d %s", status, body)
	}

	var decoded struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding the created type: %v", err)
	}
	return decoded.ID
}

// createCredential creates a credential holding theSecret and returns its id.
func (f *credentialFixture) createCredential(t *testing.T, name string) int {
	t.Helper()

	status, body := f.do(t, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name":            name,
		"organization":    f.orgA,
		"credential_type": f.typeID,
		"inputs":          map[string]string{"api_token": theSecret, "api_url": "https://api.example.com"},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the credential: %d %s", status, body)
	}

	var decoded struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding the created credential: %v", err)
	}
	return decoded.ID
}

// TestNoResponseEverCarriesASecret is the assertion the whole two-package
// split exists to make.
//
// It sweeps every route on this surface, including the ones that have
// nothing to do with credentials' values, and asserts the secret appears in
// none of their raw bytes. Sweeping rather than spot-checking is the point:
// the failure this guards against is one route somebody adds later, and a
// test that only covered the routes somebody thought of would not see it.
func TestNoResponseEverCarriesASecret(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	credID := f.createCredential(t, "prod api")

	// A template to bind it to, so the binding routes are swept too.
	ctx := context.Background()
	inv := f.client.Inventory.Create().SetName("edge").SetOrganizationID(f.orgA).SaveX(ctx)
	tmpl := f.client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(f.orgA).SetInventoryID(inv.ID).SaveX(ctx)

	requests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"list credential types", http.MethodGet, fmt.Sprintf("/api/v1/credential-types?organization=%d", f.orgA), nil},
		{"get credential type", http.MethodGet, fmt.Sprintf("/api/v1/credential-types/%d", f.typeID), nil},
		{"list credentials", http.MethodGet, fmt.Sprintf("/api/v1/credentials?organization=%d", f.orgA), nil},
		{"get credential", http.MethodGet, fmt.Sprintf("/api/v1/credentials/%d", credID), nil},
		{
			"update credential", http.MethodPatch, fmt.Sprintf("/api/v1/credentials/%d", credID),
			map[string]any{"name": "prod api", "inputs": map[string]string{"api_url": "https://other.example.com"}},
		},
		{
			"bind to a template", http.MethodPut, fmt.Sprintf("/api/v1/templates/%d/credentials", tmpl.ID),
			map[string]any{"credentials": []int{credID}},
		},
		{"list a template's credentials", http.MethodGet, fmt.Sprintf("/api/v1/templates/%d/credentials", tmpl.ID), nil},
		{
			"preview the injectors", http.MethodPost, fmt.Sprintf("/api/v1/credential-types/%d/test", f.typeID),
			map[string]any{"inputs": map[string]string{"api_token": theSecret, "api_url": "https://api.example.com"}},
		},
		{"options on the collection", http.MethodOptions, "/api/v1/credentials", nil},
		{"options on one credential", http.MethodOptions, fmt.Sprintf("/api/v1/credentials/%d", credID), nil},
	}

	for _, req := range requests {
		t.Run(req.name, func(t *testing.T) {
			status, body := f.do(t, req.method, req.path, req.body)
			if status >= 500 {
				t.Fatalf("%s answered %d: %s", req.name, status, body)
			}
			if bytes.Contains(body, []byte(theSecret)) {
				t.Fatalf("%s put the secret on the wire:\n%s", req.name, body)
			}
		})
	}
}

// TestSecretsReadBackAsTheMarker is the positive half. Absence alone would
// be satisfied by a response that omitted the field entirely, and an
// omitted field is a different and worse answer: a form rendering nothing
// would clear the stored value on its next save.
func TestSecretsReadBackAsTheMarker(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	credID := f.createCredential(t, "prod api")

	status, body := f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/credentials/%d", credID), nil)
	if status != http.StatusOK {
		t.Fatalf("GET answered %d: %s", status, body)
	}

	var decoded struct {
		Inputs map[string]string `json:"inputs"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if decoded.Inputs["api_token"] != redact.Marker {
		t.Errorf("inputs[api_token] = %q, want the redaction marker", decoded.Inputs["api_token"])
	}
	// A non-secret input must survive, or nobody can see what the
	// credential points at.
	if decoded.Inputs["api_url"] != "https://api.example.com" {
		t.Errorf("inputs[api_url] = %q, want the stored value", decoded.Inputs["api_url"])
	}
}

// TestUpdatingWithTheMarkerPreservesTheSecret covers, over real HTTP, the
// data-loss bug that would look like a successful save: a form renders the
// marker, an operator edits an unrelated field, and submitting must not
// overwrite the secret with the marker text.
func TestUpdatingWithTheMarkerPreservesTheSecret(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	credID := f.createCredential(t, "prod api")

	status, body := f.do(t, http.MethodPatch, fmt.Sprintf("/api/v1/credentials/%d", credID), map[string]any{
		"name": "prod api",
		"inputs": map[string]string{
			"api_token": redact.Marker,
			"api_url":   "https://changed.example.com",
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH answered %d: %s", status, body)
	}

	stored := f.client.Credential.GetX(context.Background(), credID)
	if stored.Inputs["api_token"] != theSecret {
		t.Fatalf("the stored secret is now %q, so an ordinary edit destroyed it", stored.Inputs["api_token"])
	}
	if stored.Inputs["api_url"] != "https://changed.example.com" {
		t.Errorf("the edited field was not saved: %v", stored.Inputs)
	}
}

// TestBindingConflictIsAConflict covers the status mapping for the binding
// rule, and that the message names both credentials so the caller can
// choose which to drop.
func TestBindingConflictIsAConflict(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	first := f.createCredential(t, "prod aws")
	second := f.createCredential(t, "dev aws")

	ctx := context.Background()
	inv := f.client.Inventory.Create().SetName("edge").SetOrganizationID(f.orgA).SaveX(ctx)
	tmpl := f.client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(f.orgA).SetInventoryID(inv.ID).SaveX(ctx)

	status, body := f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/templates/%d/credentials", tmpl.ID),
		map[string]any{"credentials": []int{first, second}})
	if status != http.StatusConflict {
		t.Fatalf("binding two credentials of one kind answered %d, want 409: %s", status, body)
	}
	for _, want := range []string{"prod aws", "dev aws"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("the conflict does not name %q: %s", want, body)
		}
	}
}

// TestManagedTypeEditIsForbidden covers the status mapping for AWX's own
// rule, which an import depends on.
func TestManagedTypeEditIsForbidden(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	managed, err := f.store.EnsureManagedType(context.Background(), managedTypeForTest())
	if err != nil {
		t.Fatalf("EnsureManagedType: %v", err)
	}

	status, body := f.do(t, http.MethodPatch, fmt.Sprintf("/api/v1/credential-types/%d", managed.ID),
		map[string]any{"name": "Renamed", "kind": "ssh"})
	if status != http.StatusForbidden {
		t.Fatalf("editing a managed type answered %d, want 403: %s", status, body)
	}

	status, body = f.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/credential-types/%d", managed.ID), nil)
	if status != http.StatusForbidden {
		t.Fatalf("deleting a managed type answered %d, want 403: %s", status, body)
	}
}

// TestInvalidInjectorIsRefusedAtTheWrite proves the API compiles injector
// templates rather than storing whatever it is given, so an author finds a
// miswritten one here rather than at somebody's launch.
func TestInvalidInjectorIsRefusedAtTheWrite(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodPost, "/api/v1/credential-types", map[string]any{
		"name":         "Broken",
		"kind":         "cloud",
		"namespace":    "broken",
		"organization": f.orgA,
		"inputs":       map[string]any{"fields": []map[string]any{{"id": "token", "label": "Token"}}},
		"injectors":    map[string]any{"env": map[string]string{"TOKEN": "{{ tokn }}"}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a type whose injector names an undeclared input answered %d, want 400: %s", status, body)
	}
}

// TestReservedEnvironmentVariableIsRefusedOverHTTP covers the injection
// hardening at the layer an attacker would actually reach it from.
func TestReservedEnvironmentVariableIsRefusedOverHTTP(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodPost, "/api/v1/credential-types", map[string]any{
		"name":         "Hostile",
		"kind":         "cloud",
		"namespace":    "hostile",
		"organization": f.orgA,
		"inputs":       map[string]any{"fields": []map[string]any{{"id": "payload", "label": "Payload"}}},
		"injectors":    map[string]any{"env": map[string]string{"LD_PRELOAD": "{{ payload }}"}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("an injector setting LD_PRELOAD answered %d, want 400: %s", status, body)
	}
}

// TestPreviewReportsShapeAndNoValues covers the authoring aid, and that it
// is an aid rather than an oracle.
func TestPreviewReportsShapeAndNoValues(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/credential-types/%d/test", f.typeID),
		map[string]any{"inputs": map[string]string{"api_token": theSecret, "api_url": "https://x"}})
	if status != http.StatusOK {
		t.Fatalf("preview answered %d: %s", status, body)
	}

	var decoded struct {
		Env        []string `json:"env"`
		ExtraVars  []string `json:"extra_vars"`
		FileLabels []string `json:"file_labels"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if len(decoded.Env) != 1 || decoded.Env[0] != "API_TOKEN" {
		t.Errorf("env = %v, want [API_TOKEN]", decoded.Env)
	}
	// Empty lists rather than null, the same contract ignored_fields draws:
	// a vanished key would make "injects no extra vars" indistinguishable
	// from "this server does not report them".
	if decoded.ExtraVars == nil || decoded.FileLabels == nil {
		t.Errorf("an empty list marshalled as null: %s", body)
	}
	if bytes.Contains(body, []byte(theSecret)) {
		t.Errorf("the preview echoed the supplied value back: %s", body)
	}
}

// managedTypeForTest is the managed type definition the forbidden-edit test
// installs. Managed types are reconciled through the store rather than
// created over HTTP, because no API caller may mint one: a type nobody can
// subsequently edit or delete is not something a request should be able to
// produce.
func managedTypeForTest() credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Machine",
		Kind:      credtype.KindSSH,
		Namespace: "ssh",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "username", Label: "Username"},
		}},
	}
}

// The error and edge paths of the credential surface.
//
// Each case asserts the STATUS a caller receives rather than the message,
// because the status is what a client branches on: a 404 tells it the
// record is gone, a 409 tells it to reconcile, and a 500 tells it to retry.
// Getting one wrong sends a client down the wrong recovery path with no
// indication anything was misclassified.

// TestMalformedIdsAndQueriesAnswerBadRequest covers the parameter parsing,
// where the failure mode is answering 500 for something the caller could
// have fixed.
func TestMalformedIdsAndQueriesAnswerBadRequest(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"a non-numeric credential type id", http.MethodGet, "/api/v1/credential-types/abc", nil},
		{"a zero credential type id", http.MethodGet, "/api/v1/credential-types/0", nil},
		{"a negative credential id", http.MethodGet, "/api/v1/credentials/-1", nil},
		{"a non-numeric credential id on delete", http.MethodDelete, "/api/v1/credentials/abc", nil},
		{"a non-numeric credential id on update", http.MethodPatch, "/api/v1/credentials/abc", map[string]any{"name": "x"}},
		{"a non-numeric template id on binding", http.MethodPut, "/api/v1/templates/abc/credentials", map[string]any{"credentials": []int{}}},
		{"a non-numeric template id on listing", http.MethodGet, "/api/v1/templates/abc/credentials", nil},
		{"a non-numeric organization filter", http.MethodGet, "/api/v1/credentials?organization=abc", nil},
		{"a missing organization on the credential list", http.MethodGet, "/api/v1/credentials", nil},
		{"a missing organization on type creation", http.MethodPost, "/api/v1/credential-types", map[string]any{"name": "x", "kind": "cloud", "namespace": "x"}},
		{"a missing credential type on credential creation", http.MethodPost, "/api/v1/credentials", map[string]any{"name": "x", "organization": 1}},
		{"a non-numeric type id on preview", http.MethodPost, "/api/v1/credential-types/abc/test", nil},
		{"a non-numeric type id on update", http.MethodPatch, "/api/v1/credential-types/abc", map[string]any{"name": "x"}},
		{"a non-numeric type id on delete", http.MethodDelete, "/api/v1/credential-types/abc", nil},
		{"a non-numeric organization on the type list", http.MethodGet, "/api/v1/credential-types?organization=abc", nil},
		{"a negative organization on the type list", http.MethodGet, "/api/v1/credential-types?organization=-1", nil},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			status, body := f.do(t, tt.method, tt.path, tt.body)
			if status != http.StatusBadRequest {
				t.Errorf("answered %d, want 400: %s", status, body)
			}
		})
	}
}

// TestMissingRecordsAnswerNotFound covers the sentinel mapping, so a client
// can tell "gone" from "broken" and stop retrying.
func TestMissingRecordsAnswerNotFound(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	const missing = 999999

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"get a missing credential type", http.MethodGet, fmt.Sprintf("/api/v1/credential-types/%d", missing), nil},
		{"update a missing credential type", http.MethodPatch, fmt.Sprintf("/api/v1/credential-types/%d", missing), map[string]any{"name": "x", "kind": "cloud"}},
		{"delete a missing credential type", http.MethodDelete, fmt.Sprintf("/api/v1/credential-types/%d", missing), nil},
		{"preview a missing credential type", http.MethodPost, fmt.Sprintf("/api/v1/credential-types/%d/test", missing), nil},
		{"get a missing credential", http.MethodGet, fmt.Sprintf("/api/v1/credentials/%d", missing), nil},
		{"update a missing credential", http.MethodPatch, fmt.Sprintf("/api/v1/credentials/%d", missing), map[string]any{"name": "x"}},
		{"delete a missing credential", http.MethodDelete, fmt.Sprintf("/api/v1/credentials/%d", missing), nil},
		{"bind against a missing template", http.MethodPut, fmt.Sprintf("/api/v1/templates/%d/credentials", missing), map[string]any{"credentials": []int{}}},
		{"create a credential of a missing type", http.MethodPost, "/api/v1/credentials", map[string]any{"name": "x", "organization": 1, "credential_type": missing}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			status, body := f.do(t, tt.method, tt.path, tt.body)
			if status != http.StatusNotFound {
				t.Errorf("answered %d, want 404: %s", status, body)
			}
		})
	}
}

// TestDuplicateNameAnswersConflict covers the uniqueness mapping, which a
// client uses to know that retrying the identical request will not help.
func TestDuplicateNameAnswersConflict(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	f.createCredential(t, "duplicate name")

	status, body := f.do(t, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name":            "duplicate name",
		"organization":    f.orgA,
		"credential_type": f.typeID,
		"inputs":          map[string]string{"api_token": "another-token-value"},
	})
	if status != http.StatusConflict {
		t.Fatalf("a duplicate name answered %d, want 409: %s", status, body)
	}
}

// TestDeletingATypeInUseAnswersConflict covers the refusal that stops a
// delete producing credentials nothing can inject.
func TestDeletingATypeInUseAnswersConflict(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	f.createCredential(t, "in use")

	status, body := f.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/credential-types/%d", f.typeID), nil)
	if status != http.StatusConflict {
		t.Fatalf("deleting a type in use answered %d, want 409: %s", status, body)
	}
}

// TestCrossTenantCredentialCreationIsForbidden covers the tenancy boundary
// at the layer a caller reaches it from.
func TestCrossTenantCredentialCreationIsForbidden(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	// orgB building a credential on orgA's custom type.
	status, body := f.do(t, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name":            "borrowed",
		"organization":    f.orgB,
		"credential_type": f.typeID,
		"inputs":          map[string]string{"api_token": "a-token-value"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("a cross-tenant credential answered %d, want 403: %s", status, body)
	}
}

// TestDeleteRemovesTheCredential covers the success path of the one handler
// with no other coverage, including that the record really is gone rather
// than merely reported as deleted.
func TestDeleteRemovesTheCredential(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	credID := f.createCredential(t, "temporary")

	status, body := f.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/credentials/%d", credID), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete answered %d, want 204: %s", status, body)
	}
	if len(body) != 0 {
		t.Errorf("a 204 carried a body: %s", body)
	}

	status, _ = f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/credentials/%d", credID), nil)
	if status != http.StatusNotFound {
		t.Errorf("the credential is still readable after deletion: %d", status)
	}
}

// TestDeleteRemovesTheCredentialType covers the same for a type with no
// credentials on it.
func TestDeleteRemovesTheCredentialType(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/credential-types/%d", f.typeID), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete answered %d, want 204: %s", status, body)
	}

	status, _ = f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/credential-types/%d", f.typeID), nil)
	if status != http.StatusNotFound {
		t.Errorf("the type is still readable after deletion: %d", status)
	}
}

// TestUpdateCredentialTypeSucceeds covers the update success path, and that
// the immutable namespace survives a body that carries a different one.
func TestUpdateCredentialTypeSucceeds(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodPatch, fmt.Sprintf("/api/v1/credential-types/%d", f.typeID), map[string]any{
		"name":      "Renamed",
		"kind":      "cloud",
		"namespace": "a_different_namespace",
		"inputs": map[string]any{
			"fields": []map[string]any{{"id": "api_token", "label": "Token", "secret": true}},
		},
		"injectors": map[string]any{"env": map[string]string{"API_TOKEN": "{{ api_token }}"}},
	})
	if status != http.StatusOK {
		t.Fatalf("update answered %d: %s", status, body)
	}

	var decoded struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if decoded.Name != "Renamed" {
		t.Errorf("name = %q, want the update applied", decoded.Name)
	}
	if decoded.Namespace == "a_different_namespace" {
		t.Error("the immutable namespace was changed by an update")
	}
}

// TestMalformedBodiesAnswerBadRequest covers the decoder, where the failure
// mode is a 500 for a caller's own typo.
func TestMalformedBodiesAnswerBadRequest(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"creating a type", http.MethodPost, "/api/v1/credential-types"},
		{"creating a credential", http.MethodPost, "/api/v1/credentials"},
		{"binding", http.MethodPut, fmt.Sprintf("/api/v1/templates/%d/credentials", 1)},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader("{not json"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("answered %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestNewCredentialHandlerRefusesIncompleteWiring pins the two panics,
// which turn a wiring mistake into a process-start failure rather than a
// first-request one.
func TestNewCredentialHandlerRefusesIncompleteWiring(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	assertHandlerPanics(t, "a nil store", func() { api.NewCredentialHandler(nil, render.New()) })
	assertHandlerPanics(t, "a nil render engine", func() { api.NewCredentialHandler(f.store, nil) })
}

// assertHandlerPanics fails unless fn panics.
func assertHandlerPanics(t *testing.T, what string, fn func()) {
	t.Helper()

	defer func() {
		if recover() == nil {
			t.Errorf("NewCredentialHandler accepted %s", what)
		}
	}()
	fn()
}

// failingCredentialStore fails every method, so the handlers' own error
// branches are reachable.
//
// A double rather than a broken database, because the question here is not
// "what does ent do when the disk fills" but "does this handler turn a
// store failure into a 500 rather than a panic, a 200 with an empty list,
// or a leaked internal error string". Those are three different bugs and
// all three look fine until a store actually fails.
type failingCredentialStore struct{ err error }

func (f failingCredentialStore) GetType(context.Context, int) (credstore.CredentialType, error) {
	return credstore.CredentialType{}, f.err
}

func (f failingCredentialStore) GetTypeByNamespace(context.Context, string) (credstore.CredentialType, error) {
	return credstore.CredentialType{}, f.err
}

func (f failingCredentialStore) ListTypes(context.Context, int) ([]credstore.CredentialType, error) {
	return nil, f.err
}

func (f failingCredentialStore) ListAllTypes(context.Context) ([]credstore.CredentialType, error) {
	return nil, f.err
}

func (f failingCredentialStore) ListAllCredentials(context.Context) ([]credstore.Credential, error) {
	return nil, f.err
}

func (f failingCredentialStore) CreateType(context.Context, int, credtype.CredentialType) (credstore.CredentialType, error) {
	return credstore.CredentialType{}, f.err
}

func (f failingCredentialStore) UpdateType(context.Context, int, credtype.CredentialType) (credstore.CredentialType, error) {
	return credstore.CredentialType{}, f.err
}

func (f failingCredentialStore) DeleteType(context.Context, int) error { return f.err }

func (f failingCredentialStore) EnsureManagedType(context.Context, credtype.CredentialType) (credstore.CredentialType, error) {
	return credstore.CredentialType{}, f.err
}

func (f failingCredentialStore) GetCredential(context.Context, int) (credstore.Credential, error) {
	return credstore.Credential{}, f.err
}

func (f failingCredentialStore) ListCredentials(context.Context, int) ([]credstore.Credential, error) {
	return nil, f.err
}

func (f failingCredentialStore) CreateCredential(context.Context, int, int, string, string, map[string]string, map[string]string, ...credstore.CredentialOption) (credstore.Credential, error) {
	return credstore.Credential{}, f.err
}

func (f failingCredentialStore) UpdateCredential(context.Context, int, string, string, map[string]string, map[string]string, ...credstore.CredentialOption) (credstore.Credential, error) {
	return credstore.Credential{}, f.err
}

func (f failingCredentialStore) DeleteCredential(context.Context, int) error { return f.err }

func (f failingCredentialStore) TemplateCredentials(context.Context, int) ([]credstore.Credential, error) {
	return nil, f.err
}

func (f failingCredentialStore) SetTemplateCredentials(context.Context, int, []int) error {
	return f.err
}

func (f failingCredentialStore) ListCredentialInputSources(context.Context, int) ([]credstore.InputSource, error) {
	return nil, f.err
}

func (f failingCredentialStore) SetCredentialInputSources(context.Context, int, []credstore.InputSourceBinding) ([]credstore.InputSource, error) {
	return nil, f.err
}

// TestAStoreFailureAnswersServerErrorWithoutLeakingIt covers every handler's
// unclassified-error branch.
//
// Two things are asserted and the second matters more. The status must be
// 500, so a client retries rather than treating a database outage as a
// permanent rejection. And the body must NOT carry the store's own error
// text: an unclassified error can name a column, a constraint, or a DSN,
// none of which a caller has any business reading.
func TestAStoreFailureAnswersServerErrorWithoutLeakingIt(t *testing.T) {
	t.Parallel()

	const internalDetail = "pq: relation \"credentials\" does not exist at 10.0.0.5:5432"
	store := failingCredentialStore{err: errors.New(internalDetail)}

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListCredentialTypes.Route(api.NewCredentialHandler(store, render.New()).ListCredentialTypes),
			apispec.GetCredentialType.Route(api.NewCredentialHandler(store, render.New()).GetCredentialType),
			apispec.CreateCredentialType.Route(api.NewCredentialHandler(store, render.New()).CreateCredentialType),
			apispec.UpdateCredentialType.Route(api.NewCredentialHandler(store, render.New()).UpdateCredentialType),
			apispec.DeleteCredentialType.Route(api.NewCredentialHandler(store, render.New()).DeleteCredentialType),
			apispec.TestCredentialType.Route(api.NewCredentialHandler(store, render.New()).TestCredentialType),
			apispec.ListCredentials.Route(api.NewCredentialHandler(store, render.New()).ListCredentials),
			apispec.GetCredential.Route(api.NewCredentialHandler(store, render.New()).GetCredential),
			apispec.CreateCredential.Route(api.NewCredentialHandler(store, render.New()).CreateCredential),
			apispec.UpdateCredential.Route(api.NewCredentialHandler(store, render.New()).UpdateCredential),
			apispec.DeleteCredentialEndpoint.Route(api.NewCredentialHandler(store, render.New()).DeleteCredential),
			apispec.ListTemplateCredentials.Route(api.NewCredentialHandler(store, render.New()).ListTemplateCredentials),
			apispec.SetTemplateCredentials.Route(api.NewCredentialHandler(store, render.New()).SetTemplateCredentials),
			apispec.ListCredentialInputSources.Route(api.NewCredentialHandler(store, render.New()).ListCredentialInputSources),
			apispec.SetCredentialInputSources.Route(api.NewCredentialHandler(store, render.New()).SetCredentialInputSources),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list types", http.MethodGet, "/api/v1/credential-types?organization=1", ""},
		{"get type", http.MethodGet, "/api/v1/credential-types/1", ""},
		{"create type", http.MethodPost, "/api/v1/credential-types", `{"name":"x","kind":"cloud","namespace":"x","organization":1}`},
		{"update type", http.MethodPatch, "/api/v1/credential-types/1", `{"name":"x","kind":"cloud"}`},
		{"delete type", http.MethodDelete, "/api/v1/credential-types/1", ""},
		{"preview type", http.MethodPost, "/api/v1/credential-types/1/test", ""},
		{"list credentials", http.MethodGet, "/api/v1/credentials?organization=1", ""},
		{"get credential", http.MethodGet, "/api/v1/credentials/1", ""},
		{"create credential", http.MethodPost, "/api/v1/credentials", `{"name":"x","organization":1,"credential_type":1}`},
		{"update credential", http.MethodPatch, "/api/v1/credentials/1", `{"name":"x"}`},
		{"delete credential", http.MethodDelete, "/api/v1/credentials/1", ""},
		{"list a template's credentials", http.MethodGet, "/api/v1/templates/1/credentials", ""},
		{"bind", http.MethodPut, "/api/v1/templates/1/credentials", `{"credentials":[1]}`},
		{"list input sources", http.MethodGet, "/api/v1/credentials/1/input-sources", ""},
		{"set input sources", http.MethodPut, "/api/v1/credentials/1/input-sources", `{"input_sources":[]}`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var reader io.Reader
			if tt.body != "" {
				reader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, reader)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("answered %d, want 500: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "pq:") || strings.Contains(rec.Body.String(), "10.0.0.5") {
				t.Errorf("the response leaked the store's own error text: %s", rec.Body.String())
			}
		})
	}
}

// TestMalformedBodiesOnEveryWriteRouteAnswerBadRequest completes the
// decoder coverage.
//
// The three routes above were the obvious ones; these are the rest. A
// handler that decoded before parsing its path id, or that treated a
// decode failure as an empty body, would answer the wrong status here and
// nowhere else.
func TestMalformedBodiesOnEveryWriteRouteAnswerBadRequest(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	credID := f.createCredential(t, "for malformed bodies")

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"updating a type", http.MethodPatch, fmt.Sprintf("/api/v1/credential-types/%d", f.typeID)},
		{"updating a credential", http.MethodPatch, fmt.Sprintf("/api/v1/credentials/%d", credID)},
		{"previewing a type", http.MethodPost, fmt.Sprintf("/api/v1/credential-types/%d/test", f.typeID)},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader("{not json"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("answered %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestPreviewWithNoBodyIsAccepted covers the deliberate optionality.
//
// Previewing with no values at all is meaningful, and refusing an empty
// request would make the simplest case the awkward one.
//
// This assertion was inverted, and the correction is worth stating rather
// than quietly making. It used to expect a 400, because the preview built
// its namespace from the supplied values alone and the renderer is
// strict-undefined, so an unsupplied input failed the render. That told an
// author their credential type was broken when it was not: a real run of
// the same type, by a credential that left the same optional input blank,
// renders fine and injects an empty value, which is also what AWX does.
// A preview that disagrees with the run is worse than no preview, since
// its whole purpose is to answer "would this work" before somebody
// launches a job. See FAILURE_PATTERNS.md #121 for the same defect on the
// injection path, which is where it was found first.
func TestPreviewWithNoBodyIsAccepted(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodPost, fmt.Sprintf("/api/v1/credential-types/%d/test", f.typeID), nil)
	if status != http.StatusOK {
		t.Fatalf("an empty preview request answered %d, want 200: %s", status, body)
	}
	// The SHAPE, which is what this endpoint reports: the variable the type
	// would set, with no value in the response.
	if !strings.Contains(string(body), "API_TOKEN") {
		t.Errorf("the preview does not report the variable this type sets: %s", body)
	}
}

// TestPreviewReportsATemplateThatCannotRender covers the 400 path, using a
// type that is genuinely unrenderable rather than one whose optional input
// was merely left blank.
//
// The reserved namespace is a declared name, so the type saves cleanly, but
// it only holds a filename when the type actually generates a file. Nothing
// but a render discovers that, which is why this endpoint renders rather
// than only listing keys.
func TestPreviewReportsATemplateThatCannotRender(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	created := map[string]any{
		"name":         "Addresses A File It Never Writes",
		"namespace":    "no_such_file",
		"kind":         "cloud",
		"organization": f.orgA,
		"inputs": map[string]any{
			"fields": []any{map[string]any{"id": "token", "label": "Token", "secret": true}},
		},
		"injectors": map[string]any{"env": map[string]any{"TOKEN": "{{ tower.filename }}"}},
	}
	status, body := f.do(t, http.MethodPost, "/api/v1/credential-types", created)
	if status != http.StatusCreated {
		t.Fatalf("creating the type answered %d: %s", status, body)
	}

	var decoded struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding the created type: %v", err)
	}

	status, body = f.do(t, http.MethodPost,
		fmt.Sprintf("/api/v1/credential-types/%d/test", decoded.ID),
		map[string]any{"inputs": map[string]string{"token": "t"}})
	if status != http.StatusBadRequest {
		t.Fatalf("answered %d, want 400: %s", status, body)
	}
	// The message names the target, so an author knows which template to
	// fix, and quotes no value.
	if !strings.Contains(string(body), "TOKEN") {
		t.Errorf("the refusal does not name the failing template: %s", body)
	}
}

// TestListCredentialTypesWithoutAnOrganization covers the unfiltered
// listing, which returns the managed catalog only.
//
// Unlike the credential list, an absent organization here is legal rather
// than a cross-tenant disclosure: a managed credential type belongs to
// nobody and its definition is not sensitive.
func TestListCredentialTypesWithoutAnOrganization(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodGet, "/api/v1/credential-types", nil)
	if status != http.StatusOK {
		t.Fatalf("an unfiltered type list answered %d: %s", status, body)
	}

	var decoded struct {
		CredentialTypes []struct {
			Namespace string `json:"namespace"`
		} `json:"credential_types"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, ct := range decoded.CredentialTypes {
		if strings.HasPrefix(ct.Namespace, "custom_api_") {
			t.Errorf("an unfiltered list returned a tenant's custom type: %s", ct.Namespace)
		}
	}
}

// TestBindingAnUnknownCredentialAnswersNotFound covers the branch where the
// template exists and one of the credentials named does not.
func TestBindingAnUnknownCredentialAnswersNotFound(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	ctx := context.Background()
	inv := f.client.Inventory.Create().SetName("edge").SetOrganizationID(f.orgA).SaveX(ctx)
	tmpl := f.client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(f.orgA).SetInventoryID(inv.ID).SaveX(ctx)

	status, body := f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/templates/%d/credentials", tmpl.ID),
		map[string]any{"credentials": []int{999999}})
	if status != http.StatusNotFound {
		t.Fatalf("binding a missing credential answered %d, want 404: %s", status, body)
	}
}

// TestUpdatingACredentialWithInvalidValuesAnswersBadRequest covers the
// validation branch on the update path, which is separate code from the
// create path's.
func TestUpdatingACredentialWithInvalidValuesAnswersBadRequest(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)
	credID := f.createCredential(t, "prod api")

	status, body := f.do(t, http.MethodPatch, fmt.Sprintf("/api/v1/credentials/%d", credID), map[string]any{
		"name":   "prod api",
		"inputs": map[string]string{"not_a_declared_input": "x"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("an undeclared input answered %d, want 400: %s", status, body)
	}
}

// TestUpdatingATypeWithAnInvalidInjectorAnswersBadRequest covers the same
// on the type update path.
func TestUpdatingATypeWithAnInvalidInjectorAnswersBadRequest(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodPatch, fmt.Sprintf("/api/v1/credential-types/%d", f.typeID), map[string]any{
		"name":      "Custom API",
		"kind":      "cloud",
		"inputs":    map[string]any{"fields": []map[string]any{{"id": "api_token", "label": "Token", "secret": true}}},
		"injectors": map[string]any{"env": map[string]string{"TOKEN": "{{ undeclared_name }}"}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("an injector naming an undeclared input answered %d, want 400: %s", status, body)
	}
}

// bindThenFailStore accepts a binding write and then fails the read that
// follows it.
//
// That sequence is the one SetTemplateCredentials performs, and the branch
// is worth covering because the wrong handling is invisible: returning the
// caller an empty credential list after a successful bind would read as
// "the binding was cleared" rather than "the response could not be built".
type bindThenFailStore struct {
	failingCredentialStore
}

func (bindThenFailStore) SetTemplateCredentials(context.Context, int, []int) error { return nil }

// TestAReadFailureAfterASuccessfulBindIsReported covers that branch.
func TestAReadFailureAfterASuccessfulBindIsReported(t *testing.T) {
	t.Parallel()

	store := bindThenFailStore{failingCredentialStore{err: errors.New("the read failed")}}
	handler := api.NewCredentialHandler(store, render.New())

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.SetTemplateCredentials.Route(handler.SetTemplateCredentials)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/templates/1/credentials", strings.NewReader(`{"credentials":[1]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a read failure after a successful bind answered %d, want 500: %s", rec.Code, rec.Body.String())
	}
	// The dangerous wrong answer is a 200 with an empty list, which reads
	// as "the binding was cleared".
	if rec.Code == http.StatusOK {
		t.Error("the handler reported success after failing to read back what it bound")
	}
}

// TestSetCredentialTypeInputsReplacesTheSchemaAndKeepsInjectors proves the
// narrowed endpoint edits the schema without disturbing the injector
// document beside it, which is the whole reason it reads the stored type
// first rather than writing what the caller sent wholesale.
func TestSetCredentialTypeInputsReplacesTheSchemaAndKeepsInjectors(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	status, body := f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/credential-types/%d/inputs", f.typeID), map[string]any{
		"inputs": map[string]any{
			"fields": []map[string]any{
				{"id": "api_token", "label": "Token", "secret": true},
				{"id": "api_url", "label": "URL"},
				{"id": "api_region", "label": "Region"},
			},
			"required": []string{"api_token"},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("setting inputs answered %d: %s", status, body)
	}

	var decoded struct {
		Inputs struct {
			Fields []struct {
				ID string `json:"id"`
			} `json:"fields"`
		} `json:"inputs"`
		Injectors struct {
			Env map[string]string `json:"env"`
		} `json:"injectors"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	found := false
	for _, fld := range decoded.Inputs.Fields {
		if fld.ID == "api_region" {
			found = true
		}
	}
	if !found {
		t.Errorf("the new input api_region is not in the returned schema: %+v", decoded.Inputs.Fields)
	}
	if decoded.Injectors.Env["API_TOKEN"] == "" {
		t.Error("replacing the inputs blanked the injector the edit never touched")
	}
}

// TestSetCredentialTypeInputsRefusesRemovingAnInjectedInput proves the
// endpoint inherits the store's whole-type validation: an injector that
// would be left pointing at an input this removed is refused here rather
// than at somebody's launch.
func TestSetCredentialTypeInputsRefusesRemovingAnInjectedInput(t *testing.T) {
	t.Parallel()

	f := newCredentialFixture(t)

	// The seeded injector reads {{ api_token }}. Dropping api_token would
	// leave it dangling, so the update must refuse.
	status, body := f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/credential-types/%d/inputs", f.typeID), map[string]any{
		"inputs": map[string]any{
			"fields": []map[string]any{{"id": "api_url", "label": "URL"}},
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("removing an injected input answered %d, want 400: %s", status, body)
	}
}
