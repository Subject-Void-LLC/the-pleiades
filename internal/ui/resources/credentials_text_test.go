// Package resources_test holds, in this file, the credential and credential
// type forms to the store's text rules: a NUL character, which SQLite stores
// and PostgreSQL refuses, is refused beside the box it was typed into rather
// than reaching the database.
package resources_test

import (
	"net/http"
	"strings"
	"testing"
)

// nulRune is a NUL, built from its code point rather than written as an
// escape a tool could turn into the real byte in this file.
var nulRune = string(rune(0))

// TestCredentialForms_RefuseANULBesideTheField posts a NUL in a name through
// each form and expects a field error, with nothing stored.
func TestCredentialForms_RefuseANULBesideTheField(t *testing.T) {
	h := newHarness(t, adminIdentity)

	t.Run("credential", func(t *testing.T) {
		form := body(t, h, "/ui/credentials/new")
		typeID := optionValue(t, form, "credential_type", "Conformance API")
		orgID := optionValue(t, body(t, h, "/ui/credentials/new?credential_type="+typeID), "organization", "acme")
		name := uniqueName(t, "nul-credential")

		w := h.post(t, "/ui/credentials", map[string]string{
			"credential_type": typeID,
			"organization":    orgID,
			"name":            name + nulRune + "tail",
			"input_api_token": "irrelevant",
		})
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "NUL") {
			t.Fatalf("a NUL in the name = %d, want 422 with a field error naming the NUL: %s", w.Code, w.Body.String())
		}
		if list := body(t, h, "/ui/credentials"); strings.Contains(list, name) {
			t.Error("a credential named with a NUL was stored")
		}
	})

	t.Run("credential type", func(t *testing.T) {
		form := body(t, h, "/ui/credential-types/new")
		orgID := optionValue(t, form, "organization", "acme")
		kind := optionValue(t, form, "kind", "cloud")
		name := uniqueName(t, "nul-type")

		w := h.post(t, "/ui/credential-types", map[string]string{
			"name":         name + nulRune + "tail",
			"kind":         kind,
			"namespace":    strings.ReplaceAll(name, "-", "_"),
			"organization": orgID,
		})
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "NUL") {
			t.Fatalf("a NUL in the name = %d, want 422 with a field error naming the NUL: %s", w.Code, w.Body.String())
		}
		if list := body(t, h, "/ui/credential-types"); strings.Contains(list, name) {
			t.Error("a credential type named with a NUL was stored")
		}
	})
}
