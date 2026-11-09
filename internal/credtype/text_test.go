// This file holds Validate to the rule that a credential type carries no NUL
// character anywhere: PostgreSQL refuses one in a character varying column
// and in a jsonb document, so a type holding one failed the write with a 500
// there while SQLite stored it.
package credtype_test

import (
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// nul is a NUL, built from its code point rather than written as an escape a
// tool could turn into the real byte in this file.
var nul = string(rune(0))

// nulBaseType is a type every case below breaks in one place.
func nulBaseType() credtype.CredentialType {
	return credtype.CredentialType{
		Name:        "Token API",
		Description: "An API reached with a token",
		Kind:        credtype.KindCloud,
		Namespace:   "token_api",
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "token", Label: "Token", Secret: true, Help: "The API token."},
				{ID: "region", Label: "Region", Choices: []string{"east", "west"}, Default: "east"},
			},
			Required: []string{"token"},
		},
		Injectors: credtype.Injectors{
			Env:       map[string]string{"API_TOKEN": "{{ token }}"},
			ExtraVars: map[string]any{"api": map[string]any{"region": "{{ region }}"}},
			File:      map[string]string{"template": "token={{ token }}"},
		},
	}
}

// TestValidateRefusesANULAnywhere puts a NUL in each place a type holds text
// and expects a refusal naming the document it is in.
func TestValidateRefusesANULAnywhere(t *testing.T) {
	if err := nulBaseType().Validate(render.New()); err != nil {
		t.Fatalf("the base type does not validate: %v", err)
	}
	cases := map[string]struct {
		field string
		edit  func(*credtype.CredentialType)
	}{
		"the name":         {"name", func(ct *credtype.CredentialType) { ct.Name += nul }},
		"the description":  {"description", func(ct *credtype.CredentialType) { ct.Description += nul }},
		"an input's label": {"inputs", func(ct *credtype.CredentialType) { ct.Inputs.Fields[0].Label += nul }},
		"an input's help":  {"inputs", func(ct *credtype.CredentialType) { ct.Inputs.Fields[0].Help += nul }},
		"a choice":         {"inputs", func(ct *credtype.CredentialType) { ct.Inputs.Fields[1].Choices[1] += nul }},
		"a default":        {"inputs", func(ct *credtype.CredentialType) { ct.Inputs.Fields[1].Default += nul }},
		"an env template":  {"injectors", func(ct *credtype.CredentialType) { ct.Injectors.Env["API_TOKEN"] += nul }},
		"an env name":      {"injectors", func(ct *credtype.CredentialType) { ct.Injectors.Env["API"+nul] = "{{ token }}" }},
		"a nested extra var": {"injectors", func(ct *credtype.CredentialType) {
			ct.Injectors.ExtraVars["api"].(map[string]any)["region"] = "x" + nul
		}},
		"a file template": {"injectors", func(ct *credtype.CredentialType) { ct.Injectors.File["template"] += nul }},
		"an omit_empty name": {"injectors", func(ct *credtype.CredentialType) {
			ct.Injectors.OmitEmpty = []string{"API_TOKEN" + nul}
		}},
		"a nested extra var key": {"injectors", func(ct *credtype.CredentialType) {
			ct.Injectors.ExtraVars["api"].(map[string]any)["zone"+nul] = "{{ region }}"
		}},
		"a required id": {"inputs", func(ct *credtype.CredentialType) { ct.Inputs.Required = []string{"token" + nul} }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ct := nulBaseType()
			tc.edit(&ct)
			err := ct.Validate(render.New())
			var text *credtype.TextError
			if !errors.As(err, &text) || text.Field != tc.field || !errors.Is(err, credtype.ErrInvalidType) {
				t.Fatalf("Validate() = %v, want a TextError on %s that is ErrInvalidType", err, tc.field)
			}
			if want := tc.field + " may not contain a NUL character: the database cannot store one"; err.Error() != want {
				t.Errorf("the refusal reads %q, want %q", err.Error(), want)
			}
		})
	}
}

// TestTextErrorSaysABlankNameIsRequired checks the other refusal's text.
func TestTextErrorSaysABlankNameIsRequired(t *testing.T) {
	err := &credtype.TextError{Field: "name", Blank: true, Kind: credtype.ErrInvalidCredential}
	if err.Error() != "name is required" || !errors.Is(err, credtype.ErrInvalidCredential) {
		t.Errorf("a blank name refusal = %q, %v", err.Error(), errors.Is(err, credtype.ErrInvalidCredential))
	}
}
