package credtype_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The first test written for this package, before any of it existed.
//
// tests/parity/testdata/credential_types/custom-rest-api-token.json is a
// real response body from a production Ascender deployment, committed
// verbatim. AWX_PARITY.md calls credential types the single biggest gap,
// and tests/parity/fields_related.go classifies all seven of this object's
// fields as gaps owned by this phase. This file is where they stop being
// gaps.
//
// The strongest claim it makes is about the decoding: the corpus object
// goes STRAIGHT into credtype.CredentialType, with no translation layer
// and no intermediate AWX-shaped DTO. That is only possible if this
// package's JSON tags are AWX's own field names, which is the point. A
// translation layer is a place for a mapping to be wrong, and this test
// failing to compile would be the first sign that one had appeared.

// corpusPath locates the committed parity fixture from this package.
func corpusPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "tests", "parity", "testdata", "credential_types", "custom-rest-api-token.json")
}

// awxListResponse is AWX's list envelope. It exists here only to reach the
// object inside it: the envelope is REST plumbing, and tests/parity's own
// field classification already records it as such.
type awxListResponse struct {
	Results []credtype.CredentialType `json:"results"`
}

// loadCorpusType decodes the one credential type in the fixture.
func loadCorpusType(t *testing.T) credtype.CredentialType {
	t.Helper()

	data, err := os.ReadFile(corpusPath(t))
	if err != nil {
		t.Fatalf("reading the parity corpus: %v", err)
	}

	var resp awxListResponse
	// Deliberately NOT DisallowUnknownFields. The fixture carries AWX's
	// REST envelope on the object itself (id, url, related, summary_fields,
	// created, modified), and refusing those would mean editing the corpus,
	// which its own README forbids: "Do not reshape a captured response
	// before committing it."
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("decoding the parity corpus into credtype.CredentialType: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("the corpus holds %d credential types, want 1", len(resp.Results))
	}
	return resp.Results[0]
}

// TestCorpusDecodesWithoutATranslationLayer asserts every field the parity
// suite classifies as a gap arrives intact.
func TestCorpusDecodesWithoutATranslationLayer(t *testing.T) {
	t.Parallel()

	ct := loadCorpusType(t)

	if ct.Name != "Custom REST API Token" {
		t.Errorf("Name = %q", ct.Name)
	}
	if ct.Description != "Bearer token and base URL for custom REST API integrations" {
		t.Errorf("Description = %q", ct.Description)
	}
	if ct.Kind != credtype.KindCloud {
		t.Errorf("Kind = %q, want %q", ct.Kind, credtype.KindCloud)
	}
	if ct.Namespace != "custom_api_token" {
		t.Errorf("Namespace = %q", ct.Namespace)
	}
	if ct.Managed {
		t.Error("Managed = true, want false: this is a custom type, and a managed one cannot be edited")
	}

	if len(ct.Inputs.Fields) != 2 {
		t.Fatalf("Inputs.Fields has %d entries, want 2", len(ct.Inputs.Fields))
	}

	token := ct.Inputs.Fields[0]
	if token.ID != "api_token" || token.Label != "API Bearer Token" {
		t.Errorf("first field = %+v", token)
	}
	if token.Type != credtype.InputString {
		t.Errorf("first field Type = %q, want %q", token.Type, credtype.InputString)
	}
	if !token.Secret {
		t.Error("api_token is not marked secret, and secret is what decides encryption at rest and redaction on the way out")
	}

	url := ct.Inputs.Fields[1]
	if url.ID != "api_url" || url.Label != "API Base URL" {
		t.Errorf("second field = %+v", url)
	}
	if url.Secret {
		t.Error("api_url is marked secret, but the corpus does not mark it so")
	}

	if got := ct.Inputs.Required; len(got) != 2 || got[0] != "api_token" || got[1] != "api_url" {
		t.Errorf("Inputs.Required = %v, want [api_token api_url]", got)
	}

	wantEnv := map[string]string{
		"REST_API_TOKEN": "{{ api_token }}",
		"REST_API_URL":   "{{ api_url }}",
	}
	for k, want := range wantEnv {
		if got := ct.Injectors.Env[k]; got != want {
			t.Errorf("Injectors.Env[%q] = %q, want %q", k, got, want)
		}
	}
	if got := ct.Injectors.ExtraVars["ansible_api_token"]; got != "{{ api_token }}" {
		t.Errorf("Injectors.ExtraVars[ansible_api_token] = %v, want %q", got, "{{ api_token }}")
	}
	if len(ct.Injectors.File) != 0 {
		t.Errorf("Injectors.File = %v, want none", ct.Injectors.File)
	}
}

// TestCorpusTypeValidates asserts the real type passes this package's own
// validation. A validator that rejects a credential type a customer
// actually has is worse than no validator, because it blocks the migration
// this whole phase exists to enable.
func TestCorpusTypeValidates(t *testing.T) {
	t.Parallel()

	ct := loadCorpusType(t)
	if err := ct.Validate(render.New()); err != nil {
		t.Fatalf("a real AWX credential type failed validation: %v", err)
	}
}

// TestCorpusSecretFieldsNamesOnlyTheSecretOne pins the seam that decides
// what gets encrypted at rest and redacted on the way out.
//
// launch.Survey.SecretVariables is the same seam for survey answers, and
// both exist so that exactly one place per entity decides what is secret.
// Two places would eventually disagree, and the disagreement would be
// silent in the dangerous direction.
func TestCorpusSecretFieldsNamesOnlyTheSecretOne(t *testing.T) {
	t.Parallel()

	got := loadCorpusType(t).Inputs.SecretFields()
	if len(got) != 1 || got[0] != "api_token" {
		t.Errorf("SecretFields() = %v, want [api_token]", got)
	}
}

// TestCorpusInjectorsNameOnlyDeclaredInputs is the check that makes
// strict-undefined rendering safe at run time.
//
// The renderer refuses a name it was not given, which is right, but a
// launch is the wrong moment to discover that a credential type references
// an input it never declared. Template.Names lets that be caught when the
// type is saved instead, and this asserts the real corpus type satisfies
// it.
func TestCorpusInjectorsNameOnlyDeclaredInputs(t *testing.T) {
	t.Parallel()

	ct := loadCorpusType(t)
	if err := ct.Injectors.Validate(ct.Inputs, render.New()); err != nil {
		t.Fatalf("the corpus type's injectors reference something it does not declare: %v", err)
	}
}
