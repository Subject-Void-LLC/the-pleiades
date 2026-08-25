package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// The input-source routes over real HTTP, through the real router with the
// real store behind it.
//
// The property worth stating up front is the one this whole surface exists
// under: these routes describe WHERE a value lives and never carry the
// value, because the platform does not store it. The handler holds
// credstore.Store, whose projection has no field a plaintext secret could
// occupy, so that is structural rather than something a reviewer checks.

// externalTypeAndSource adds an external-kind type and one credential of it
// to the fixture, which is what the far end of every binding needs.
func externalTypeAndSource(t *testing.T, f *credentialFixture) int {
	t.Helper()

	ctx := context.Background()
	created, err := f.store.CreateType(ctx, f.orgA, credtype.CredentialType{
		Name:      "Test Vault",
		Kind:      credtype.KindExternal,
		Namespace: "test_vault",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: "token", Label: "Token", Secret: true}},
			Required: []string{"token"},
		},
	})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	source, err := f.store.CreateCredential(ctx, f.orgA, created.ID, "prod vault", "",
		map[string]string{"token": "s.roottoken"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential: %v", err)
	}
	return source.ID
}

// TestInputSourcesRoundTripOverHTTP is the base case for both routes.
func TestInputSourcesRoundTripOverHTTP(t *testing.T) {
	f := newCredentialFixture(t)
	sourceID := externalTypeAndSource(t, f)
	credID := f.createCredential(t, "prod api")

	status, body := f.do(t, http.MethodPut, credentialInputSourcesPath(credID), map[string]any{
		"input_sources": []map[string]any{{
			"input_id":          "api_token",
			"source_credential": sourceID,
			"metadata":          map[string]string{"path": "secret/data/prod", "key": "token"},
		}},
	})
	if status != http.StatusOK {
		t.Fatalf("PUT input-sources = %d, want 200: %s", status, body)
	}

	// The source's own token must not appear anywhere in the response. It
	// is the secret this whole surface exists to avoid storing, and a
	// binding response naming it would put it in a log and a browser
	// history.
	if strings.Contains(string(body), "s.roottoken") {
		t.Fatalf("the response carries the source credential's token: %s", body)
	}

	status, body = f.do(t, http.MethodGet, credentialInputSourcesPath(credID), nil)
	if status != http.StatusOK {
		t.Fatalf("GET input-sources = %d, want 200: %s", status, body)
	}

	var got struct {
		InputSources []struct {
			InputID                   string            `json:"input_id"`
			SourceCredential          int               `json:"source_credential"`
			SourceCredentialName      string            `json:"source_credential_name"`
			SourceCredentialNamespace string            `json:"source_credential_namespace"`
			Metadata                  map[string]string `json:"metadata"`
		} `json:"input_sources"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if len(got.InputSources) != 1 {
		t.Fatalf("GET returned %d bindings, want one: %s", len(got.InputSources), body)
	}
	one := got.InputSources[0]
	if one.InputID != "api_token" || one.SourceCredential != sourceID {
		t.Errorf("binding = %+v, want api_token bound to the source", one)
	}
	if one.SourceCredentialName != "prod vault" || one.SourceCredentialNamespace != "test_vault" {
		t.Errorf("binding = %+v, want the source named so a reader needs no second request", one)
	}
	// Metadata is returned unredacted, deliberately: a path is a pointer to
	// a secret rather than a secret, and hiding it would make "which
	// credentials point at this mount" unanswerable during a migration.
	if one.Metadata["path"] != "secret/data/prod" {
		t.Errorf("metadata = %v, want the path returned", one.Metadata)
	}
}

// TestAnEmptySetOverHTTPUnbindsEverything covers the replace semantics at
// the route, since that is where a caller could be surprised by it.
func TestAnEmptySetOverHTTPUnbindsEverything(t *testing.T) {
	f := newCredentialFixture(t)
	sourceID := externalTypeAndSource(t, f)
	credID := f.createCredential(t, "prod api")

	if status, body := f.do(t, http.MethodPut, credentialInputSourcesPath(credID), map[string]any{
		"input_sources": []map[string]any{{"input_id": "api_token", "source_credential": sourceID}},
	}); status != http.StatusOK {
		t.Fatalf("PUT input-sources = %d, want 200: %s", status, body)
	}

	status, body := f.do(t, http.MethodPut, credentialInputSourcesPath(credID), map[string]any{
		"input_sources": []map[string]any{},
	})
	if status != http.StatusOK {
		t.Fatalf("PUT empty = %d, want 200: %s", status, body)
	}
	var got struct {
		InputSources []json.RawMessage `json:"input_sources"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if len(got.InputSources) != 0 {
		t.Fatalf("PUT with an empty list left %d bindings, want none: %s", len(got.InputSources), body)
	}
}

// TestInputSourceRefusalsAnswerTheRightStatus is what makes the store's
// refusals usable by a caller: a 404 and a 409 send somebody to different
// places, and both are wrong if the answer is 500.
func TestInputSourceRefusalsAnswerTheRightStatus(t *testing.T) {
	f := newCredentialFixture(t)
	sourceID := externalTypeAndSource(t, f)
	credID := f.createCredential(t, "prod api")

	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "an input the type does not declare",
			body: map[string]any{"input_sources": []map[string]any{
				{"input_id": "not_an_input", "source_credential": sourceID},
			}},
			want: http.StatusNotFound,
		},
		{
			name: "a source that does not exist",
			body: map[string]any{"input_sources": []map[string]any{
				{"input_id": "api_token", "source_credential": 999999},
			}},
			want: http.StatusNotFound,
		},
		{
			name: "the same input bound twice",
			body: map[string]any{"input_sources": []map[string]any{
				{"input_id": "api_token", "source_credential": sourceID},
				{"input_id": "api_token", "source_credential": sourceID},
			}},
			want: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := f.do(t, http.MethodPut, credentialInputSourcesPath(credID), tt.body)
			if status != tt.want {
				t.Fatalf("PUT input-sources = %d, want %d: %s", status, tt.want, body)
			}
		})
	}
}

// TestACycleOverHTTPIsAConflict pins the status the cycle refusal answers
// with, which is the one an operator's client has to branch on.
func TestACycleOverHTTPIsAConflict(t *testing.T) {
	f := newCredentialFixture(t)
	sourceID := externalTypeAndSource(t, f)

	status, body := f.do(t, http.MethodPut, credentialInputSourcesPath(sourceID), map[string]any{
		"input_sources": []map[string]any{
			{"input_id": "token", "source_credential": sourceID},
		},
	})
	if status != http.StatusConflict {
		t.Fatalf("binding a credential to itself = %d, want 409: %s", status, body)
	}
}

// TestMalformedInputSourceRequestsAnswerBadRequest covers the two shapes a
// broken client produces: a bad id in the path and a body that is not JSON.
func TestMalformedInputSourceRequestsAnswerBadRequest(t *testing.T) {
	f := newCredentialFixture(t)
	credID := f.createCredential(t, "prod api")

	if status, body := f.do(t, http.MethodGet, "/api/v1/credentials/not-a-number/input-sources", nil); status != http.StatusBadRequest {
		t.Errorf("GET with a non-numeric id = %d, want 400: %s", status, body)
	}
	if status, body := f.do(t, http.MethodPut, credentialInputSourcesPath(credID), "not an object"); status != http.StatusBadRequest {
		t.Errorf("PUT with a malformed body = %d, want 400: %s", status, body)
	}
	if status, body := f.do(t, http.MethodGet, "/api/v1/credentials/999999/input-sources", nil); status != http.StatusOK {
		// A credential that does not exist simply has no bindings. This is
		// a listing rather than a fetch, and an empty list is the honest
		// answer for "what is bound to nothing".
		t.Errorf("GET for an unknown credential = %d, want 200 with an empty list: %s", status, body)
	}
}

// credentialInputSourcesPath is the route under test, built once so a
// change to it is a change in one place.
func credentialInputSourcesPath(credentialID int) string {
	return "/api/v1/credentials/" + strconv.Itoa(credentialID) + "/input-sources"
}
