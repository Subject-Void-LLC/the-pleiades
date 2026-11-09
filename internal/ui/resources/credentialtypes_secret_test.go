// Package resources_test holds, in this file, the Credential Types view to the
// rule that a value stored as secret is never shown as plain, through the
// view's own controls and the real credential store the views run on.
package resources_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// uiNoteCanary is the secret the test below stores under an input it then
// removes and adds back through the Credential Types view.
const uiNoteCanary = "sk-live-UI-NOTE-CANARY-8e7d6c5b4a39"

// TestCredentialTypes_RemovingAndAddingBackAnInputNeverShowsItsSecret drives
// the two-step exposure through the view's own row and header controls: a
// secret input is removed, then added back with the secret box unticked.
// Before the store deleted a removed input's values, the credential's edit
// form then showed the stored secret in plain text.
func TestCredentialTypes_RemovingAndAddingBackAnInputNeverShowsItsSecret(t *testing.T) {
	h := newHarness(t, adminIdentity)
	ctx := context.Background()
	store := conformanceCredentialStore

	namespace := strings.ReplaceAll(uniqueName(t, "noted"), "-", "_")
	ct, err := store.CreateType(ctx, conformanceCredentialOrgID, credtype.CredentialType{
		Name:      "Noted " + namespace,
		Kind:      credtype.KindCloud,
		Namespace: namespace,
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
				{ID: "note", Label: "Note", Secret: true},
			},
			Required: []string{"api_token"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"API_TOKEN": "{{ api_token }}"}},
	})
	if err != nil {
		t.Fatalf("CreateType() = %v", err)
	}
	cred, err := store.CreateCredential(ctx, conformanceCredentialOrgID, ct.ID, "noted "+namespace, "",
		map[string]string{"api_token": credentialCanary, "note": uiNoteCanary}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() = %v", err)
	}
	typeID := strconv.Itoa(ct.ID)

	if w := h.post(t, "/ui/credential-types/"+typeID+"/remove-input/note", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("removing note = %d, want a redirect: %s", w.Code, w.Body.String())
	}
	if w := h.post(t, "/ui/credential-types/"+typeID+"/add-input", map[string]string{
		"id": "note", "label": "Note", "type": "string",
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("adding note back = %d, want a redirect: %s", w.Code, w.Body.String())
	}

	// The edit form prefills every input as the store's read gives it, so a
	// value stored under a now non-secret input would be in its value
	// attribute; the detail page names inputs without their values.
	for _, path := range []string{"/ui/credentials/" + strconv.Itoa(cred.ID), "/ui/credentials/" + strconv.Itoa(cred.ID) + "/edit"} {
		w := h.get(t, path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), uiNoteCanary) {
			t.Errorf("GET %s shows the secret stored before its input was removed and added back", path)
		}
	}
	// The read is the redacted one, which shows a non-secret input's value as
	// stored, so an absent key here is an absent key in the database.
	got, err := store.GetCredential(ctx, cred.ID)
	if err != nil {
		t.Fatalf("GetCredential() = %v", err)
	}
	if value, held := got.Inputs["note"]; held {
		t.Errorf("the credential still stores a note (%d bytes) after it was removed", len(value))
	}
}
