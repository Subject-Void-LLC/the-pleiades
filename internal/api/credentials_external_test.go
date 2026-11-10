// This file holds the credential writes to the rule that a value read from
// an external secret source lands only in an input the type marks secret:
// the masking ruleset is built from the type's secret fields, so a secret
// resolved into any other input reached the run in plain text. A reference
// string and an external-kind input source are both refused at the write.
package api_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCredentials_RefuseAnExternalSourceIntoAPlainInput drives both write
// forms against the patch type's plain url input.
func TestCredentials_RefuseAnExternalSourceIntoAPlainInput(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.createPatchCredential(t, map[string]any{
		"name": "edge api", "organization": f.orgA, "credential_type": f.createPatchType(t, f.orgA, "patch_api"),
		"inputs": map[string]string{"token": patchTokenSecret},
	})

	t.Run("a reference string on PATCH", func(t *testing.T) {
		f.credentialRefusedUnchanged(t, id, `{"external":{"url":"file:edge_secret"}}`,
			http.StatusBadRequest, `\"url\"`, "not secret")
	})

	t.Run("a reference string on create", func(t *testing.T) {
		status, body := f.do(t, http.MethodPost, "/api/v1/credentials", map[string]any{
			"name": "referenced", "organization": f.orgA, "credential_type": f.createPatchType(t, f.orgA, "patch_api_create"),
			"inputs": map[string]string{"token": patchTokenSecret}, "external": map[string]string{"url": "file:edge_secret"},
		})
		if status != http.StatusBadRequest {
			t.Errorf("POST with a reference on a plain input = %d %s, want 400", status, body)
		}
	})

	t.Run("an external-kind input source", func(t *testing.T) {
		status, body := f.do(t, http.MethodPost, "/api/v1/credential-types", map[string]any{
			"name": "Vault", "kind": "external", "namespace": "patch_vault", "organization": f.orgA,
			"inputs": map[string]any{"fields": []map[string]any{{"id": "token", "label": "Token", "secret": true}}},
		})
		if status != http.StatusCreated {
			t.Fatalf("creating the external-kind type: %d %s", status, body)
		}
		vault := f.createPatchCredential(t, map[string]any{
			"name": "vault", "organization": f.orgA, "credential_type": decodeID(t, body),
			"inputs": map[string]string{"token": patchRotatedSecret},
		})
		status, body = f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/credentials/%d/input-sources", id), map[string]any{
			"input_sources": []map[string]any{{"input_id": "url", "source_credential": vault, "metadata": map[string]string{"secret_path": "kv/edge"}}},
		})
		if status != http.StatusConflict {
			t.Errorf("binding a plain input to an external-kind source = %d %s, want 409", status, body)
		}
		assertNoSecret(t, "PUT input-sources", body)
	})
}
