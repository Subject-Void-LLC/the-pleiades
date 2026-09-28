// Tests for the Users view's own field check, beyond the conformance suite.
package resources_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestUsersView_RefusesAnUnusableAddressAtTheControl covers the one field
// check the users form carries beyond the conformance suite: an address
// holding a character an address cannot hold is refused on the form, with
// the message on the email control, and no user is created. The store
// refuses it as well, through auth.NormalizeEmail; the form check exists
// only so the refusal names the field instead of failing the whole page.
func TestUsersView_RefusesAnUnusableAddressAtTheControl(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, email := range []string{
		"adm\x00in@example.test",
		"adm\tin@example.test",
		"a\xffb@example.test",
		"admin" + string(rune(0x202e)) + "@example.test",
	} {
		w := h.post(t, "/ui/users", map[string]string{"email": email})
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("POST /ui/users with %q = %d, want 422: %s", email, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "holds a character an address cannot") {
			t.Errorf("POST /ui/users with %q did not name the email control's problem", email)
		}
	}

	// And an ordinary address still goes through, so the refusal above is
	// about the characters rather than the form. Unique per run, since this
	// package's database outlives a test (see uniqueName).
	if w := h.post(t, "/ui/users", map[string]string{"email": uniqueName(t, "new-operator") + "@example.test"}); w.Code != http.StatusSeeOther {
		t.Fatalf("POST /ui/users with an ordinary address = %d, want 303: %s", w.Code, w.Body.String())
	}
}
