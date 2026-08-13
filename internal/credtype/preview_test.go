package credtype_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestPreviewReportsShapeWithoutValues covers the authoring aid behind
// POST /credential-types/{id}/test.
//
// The assertion that matters is the negative one: the preview renders every
// template for real, which is what proves the document works, and then
// reports only keys. Returning the rendered values would turn an authoring
// aid into an oracle that reads back whatever the caller supplied.
func TestPreviewReportsShapeWithoutValues(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{Fields: []credtype.InputField{
		{ID: "api_token", Label: "Token", Secret: true},
		{ID: "api_url", Label: "URL"},
		{ID: "region", Label: "Region", Default: "us-east-1"},
	}}
	inj := credtype.Injectors{
		Env: map[string]string{
			"API_TOKEN": "{{ api_token }}",
			"API_URL":   "{{ api_url }}",
			"REGION":    "{{ region }}",
		},
		ExtraVars: map[string]any{
			"flat":  "{{ api_token }}",
			"outer": map[string]any{"inner": "{{ api_url }}"},
		},
	}

	got, err := inj.Preview(schema, render.New(), map[string]string{
		"api_token": "a-dummy-token",
		"api_url":   "https://x",
	})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	wantEnv := []string{"API_TOKEN", "API_URL", "REGION"}
	if len(got.Env) != len(wantEnv) {
		t.Fatalf("Env = %v, want %v", got.Env, wantEnv)
	}
	for i, want := range wantEnv {
		if got.Env[i] != want {
			t.Errorf("Env = %v, want %v sorted", got.Env, wantEnv)
		}
	}

	// Nested keys are reported dotted, so an author can see which leaf a
	// template belongs to rather than only that a nested map exists.
	wantVars := []string{"flat", "outer.inner"}
	if len(got.ExtraVars) != len(wantVars) {
		t.Fatalf("ExtraVars = %v, want %v", got.ExtraVars, wantVars)
	}
	for i, want := range wantVars {
		if got.ExtraVars[i] != want {
			t.Errorf("ExtraVars = %v, want %v sorted", got.ExtraVars, wantVars)
		}
	}
}

// TestPreviewFillsDefaultsBeforeRendering covers why a preview does not
// report an undefined-variable failure for an input the type itself would
// have answered.
func TestPreviewFillsDefaultsBeforeRendering(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{Fields: []credtype.InputField{
		{ID: "region", Label: "Region", Default: "us-east-1"},
	}}
	inj := credtype.Injectors{Env: map[string]string{"REGION": "{{ region }}"}}

	// No values supplied at all, which is the ordinary case for a type
	// whose inputs all declare defaults.
	if _, err := inj.Preview(schema, render.New(), nil); err != nil {
		t.Fatalf("Preview() with no supplied values error = %v", err)
	}
}

// TestPreviewReportsATemplateThatCannotRender is the whole reason the
// preview renders rather than only listing keys.
func TestPreviewReportsATemplateThatCannotRender(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{Fields: []credtype.InputField{
		{ID: "api_token", Label: "Token", Secret: true},
	}}
	// Valid against the schema, so Validate accepts it, and unrenderable
	// with these values, which only a render can discover.
	inj := credtype.Injectors{Env: map[string]string{"TOKEN": "{{ api_token }}"}}

	_, err := inj.Preview(schema, render.New(), nil)
	if err == nil {
		t.Fatal("Preview() succeeded with no value for a required input")
	}
	if !errors.Is(err, credtype.ErrInvalidType) {
		t.Errorf("error = %v, want one matching ErrInvalidType", err)
	}
	// The message has to name the target so an author knows which template
	// to fix.
	if !strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("the error does not name the failing template: %v", err)
	}
}

// TestPreviewResolvesTheReservedNamespace covers both file spellings, which
// resolve the reserved namespace to different SHAPES: a value for the
// single-file form, a map for the multi-file one. A preview that supplied
// only one would report a spurious failure for every type using the other.
func TestPreviewResolvesTheReservedNamespace(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{Fields: []credtype.InputField{
		{ID: "api_token", Label: "Token", Secret: true},
	}}

	tests := []struct {
		name       string
		inj        credtype.Injectors
		wantLabels []string
	}{
		{
			name: "the single-file spelling",
			inj: credtype.Injectors{
				File: map[string]string{"template": "{{ api_token }}"},
				Env:  map[string]string{"CONFIG": "{{ tower.filename }}"},
			},
			wantLabels: []string{""},
		},
		{
			name: "the multi-file spelling",
			inj: credtype.Injectors{
				File: map[string]string{"template.cert": "{{ api_token }}", "template.key": "{{ api_token }}"},
				Env:  map[string]string{"CERT": "{{ tower.filename.cert }}", "KEY": "{{ tower.filename.key }}"},
			},
			wantLabels: []string{"cert", "key"},
		},
		{
			name: "the native spelling of the namespace",
			inj: credtype.Injectors{
				File: map[string]string{"template": "{{ api_token }}"},
				Env:  map[string]string{"CONFIG": "{{ pleiades.filename }}"},
			},
			wantLabels: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.inj.Preview(schema, render.New(), map[string]string{"api_token": "a-dummy-token"})
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			if len(got.FileLabels) != len(tt.wantLabels) {
				t.Fatalf("FileLabels = %v, want %v", got.FileLabels, tt.wantLabels)
			}
			for i, want := range tt.wantLabels {
				if got.FileLabels[i] != want {
					t.Errorf("FileLabels = %v, want %v", got.FileLabels, tt.wantLabels)
				}
			}
		})
	}
}

// TestPreviewRequiresARenderEngine pins the nil case, since a preview that
// skipped rendering would report success for a document that cannot render,
// which is the one thing it exists to catch.
func TestPreviewRequiresARenderEngine(t *testing.T) {
	t.Parallel()

	if _, err := (credtype.Injectors{}).Preview(credtype.InputSchema{}, nil, nil); err == nil {
		t.Fatal("Preview() with no render engine succeeded")
	}
}

// TestPreviewOnAnEmptyDocumentIsEmpty covers the ssh machine credential
// shape: a type that injects through a Go object rather than through any of
// the three data targets.
func TestPreviewOnAnEmptyDocumentIsEmpty(t *testing.T) {
	t.Parallel()

	got, err := (credtype.Injectors{}).Preview(credtype.InputSchema{}, render.New(), nil)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(got.Env) != 0 || len(got.ExtraVars) != 0 || len(got.FileLabels) != 0 {
		t.Errorf("Preview() of an empty document = %+v, want nothing", got)
	}
}

// TestResolvedCredentialAccessors covers the projections the injector and
// the binding rule read off a resolved credential.
func TestResolvedCredentialAccessors(t *testing.T) {
	t.Parallel()

	cred := credtype.Credential{
		ID:   7,
		Name: "prod vault",
		Type: credtype.CredentialType{
			Name:      "Vault",
			Kind:      credtype.KindVault,
			Namespace: "vault",
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "vault_password", Label: "Password", Secret: true},
				{ID: "vault_id", Label: "Identifier"},
			}},
		},
		Inputs: map[string]string{"vault_password": "a-vault-password", "vault_id": "prod"},
	}

	t.Run("Bound reads the vault identifier from the real values", func(t *testing.T) {
		// It has to come from the real values rather than a projection,
		// because vault_id may itself be a secret field and a redacted one
		// would make every vault credential look like the same identity.
		b := cred.Bound()
		if b.CredentialID != 7 || b.CredentialName != "prod vault" {
			t.Errorf("Bound() = %+v", b)
		}
		if b.Kind != credtype.KindVault || b.VaultIdentifier != "prod" {
			t.Errorf("Bound() = %+v", b)
		}
	})

	t.Run("RenderVars binds every input", func(t *testing.T) {
		vars := cred.RenderVars()
		if vars["vault_password"] != "a-vault-password" || vars["vault_id"] != "prod" {
			t.Errorf("RenderVars() = %v", vars)
		}
		// The reserved namespace is deliberately absent: the paths it holds
		// do not exist until the files they name have been decided.
		if _, present := vars[credtype.ReservedTower]; present {
			t.Error("RenderVars() carried the reserved namespace, whose paths are not known yet")
		}
	})

	t.Run("Validate checks the values against the type", func(t *testing.T) {
		if err := cred.Validate(); err != nil {
			t.Errorf("Validate() on a complete credential error = %v", err)
		}

		typeless := credtype.Credential{Name: "orphan"}
		if err := typeless.Validate(); err == nil {
			t.Error("Validate() accepted a credential carrying no type")
		}

		wrong := cred
		wrong.Inputs = map[string]string{"nonexistent": "x"}
		if err := wrong.Validate(); err == nil {
			t.Error("Validate() accepted a value for an input the type does not declare")
		}
	})
}

// TestBindingSummary covers the shared description helper, which exists so
// the API and the UI describe a template's credentials the same way rather
// than each formatting the list themselves.
func TestBindingSummary(t *testing.T) {
	t.Parallel()

	got := credtype.BindingSummary([]credtype.Bound{
		{CredentialName: "machine", Kind: credtype.KindSSH},
		{CredentialName: "prod vault", Kind: credtype.KindVault, VaultIdentifier: "prod"},
		{CredentialName: "default vault", Kind: credtype.KindVault},
	})

	want := []string{
		"default vault (vault)",
		"machine (ssh)",
		"prod vault (vault: prod)",
	}
	if len(got) != len(want) {
		t.Fatalf("BindingSummary() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BindingSummary() = %v, want %v sorted", got, want)
		}
	}
}

// TestSecretValuesSkipsAnEmptySecret covers the branch that keeps an unset
// secret out of the masking set. Registering "" would match the gap between
// every pair of characters in every later line.
func TestSecretValuesSkipsAnEmptySecret(t *testing.T) {
	t.Parallel()

	cred := credtype.Credential{
		Type: credtype.CredentialType{Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "set", Label: "Set", Secret: true},
			{ID: "unset", Label: "Unset", Secret: true},
		}}},
		Inputs: map[string]string{"set": "a-real-secret-value", "unset": ""},
	}

	got := cred.SecretValues()
	if len(got) != 1 || got[0] != "a-real-secret-value" {
		t.Errorf("SecretValues() = %v, want only the value that is set", got)
	}
}
