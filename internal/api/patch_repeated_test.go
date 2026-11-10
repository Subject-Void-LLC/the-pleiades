// This file holds every PATCH body to one rule: a field named twice, in any
// mix of upper and lower case, is refused, naming the field by its own tag.
//
// encoding/json matches a key to a field without regard to case and lets a
// later key win, and for a field whose value is decoded by its own
// unmarshaler into a pointer it already set (a credential's external, a
// template's survey) it decodes the second into the first, so a body naming
// external twice stored both sets of references.
package api_test

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// kelvin is the KELVIN SIGN, which encoding/json folds to "k" when it
// matches a key to a field, built from its code point.
var kelvin = string(rune(0x212A))

// TestPatch_RefusesAFieldNamedTwice drives one route of each PATCH family.
func TestPatch_RefusesAFieldNamedTwice(t *testing.T) {
	t.Parallel()

	t.Run("template", func(t *testing.T) {
		f := newTemplateFixture(t)
		created := f.createTemplate(t, f.fullTemplateBody("patch"))
		for body, field := range map[string]string{
			`{"description":"one","DESCRIPTION":"two"}`:              `\"description\"`,
			`{"survey":{"enabled":false,"Enabled":true}}`:            `\"survey.enabled\"`,
			`{"survey":{"enabled":false},"Survey":{"questions":[]}}`: `\"survey\"`,
		} {
			before := f.readTemplate(t, created.ID)
			status, resp := f.patchTemplate(t, created.ID, body)
			if status != http.StatusBadRequest || !containsAll(resp, field, "more than once") {
				t.Errorf("PATCH %s: %d %s, want 400 naming %s", body, status, resp, field)
			}
			if after := f.readTemplate(t, created.ID); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused PATCH %s changed the template", body)
			}
		}
	})

	t.Run("organization", func(t *testing.T) {
		router, _ := accessRouter(t)
		org := accessPatchCases(t, router)[0]
		assertRefusedUnchanged(t, router, org.target, `{"description":"one","Description":"two"}`,
			http.StatusBadRequest, `\"description\"`, "more than once")
	})

	t.Run("project", func(t *testing.T) {
		f := newProjectFixture(t)
		id := f.createProject(t, "automation")
		f.refusedUnchanged(t, id, `{"description":"one","description":"two"}`,
			http.StatusBadRequest, `\"description\"`, "more than once")
	})

	t.Run("credential type", func(t *testing.T) {
		f := newCredentialFixture(t)
		typeID := f.createPatchType(t, f.orgA, "patch_api")
		f.credentialTypeRefusedUnchanged(t, typeID, `{"name":"one","NAME":"two"}`,
			http.StatusBadRequest, `\"name\"`, "more than once")
		f.credentialTypeRefusedUnchanged(t, typeID, `{"kind":"token","`+kelvin+`IND":"cloud"}`,
			http.StatusBadRequest, `\"kind\"`, "more than once")
	})

	t.Run("credential", func(t *testing.T) {
		f := newCredentialFixture(t)
		id := f.edgeCredential(t, f.createPatchType(t, f.orgA, "patch_api"))
		f.credentialRefusedUnchanged(t, id,
			`{"external":{"url":"file:one"},"EXTERNAL":{"token":"file:two"}}`,
			http.StatusBadRequest, `\"external\"`, "more than once")
		f.credentialRefusedUnchanged(t, id, fmt.Sprintf(`{"inputs":{"region":"west"},"Inputs":{"backup":%q}}`, ""),
			http.StatusBadRequest, `\"inputs\"`, "more than once")
	})
}

// containsAll reports whether s contains every one of parts.
func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
