// This file covers the credential type write path through the real router:
// a custom type is created, its metadata edited without losing its schema,
// and deleted, while a managed type offers none of those. The store is the
// authority on each refusal; these prove the view surfaces them the way a
// person meets them rather than as a 500 or a control that cannot work.
package resources_test

import (
	"net/http"
	"strings"
	"testing"
)

// editLink is the affordance a detail page renders only when the record may
// be edited, and confirmDeleteDialog the one it renders only when it may be
// deleted. Both are gated by Descriptor.Applies, which withdraws them for a
// managed type.
func editLink(name, id string) string { return `href="/ui/` + name + `/` + id + `/edit"` }

const confirmDeleteDialog = `id="confirm-delete"`

// firstManagedTypeID returns a credential type the UI offers no edit for,
// which is how a managed type presents: shipped by the platform, refused by
// the store, and stripped of both write affordances.
func firstManagedTypeID(t *testing.T, h *harness) string {
	t.Helper()
	for _, id := range recordIDs(t, h, "credential-types") {
		detail := h.get(t, "/ui/credential-types/"+id).Body.String()
		if !strings.Contains(detail, editLink("credential-types", id)) {
			return id
		}
	}
	return ""
}

func TestCredentialTypesForm_CreatesACustomType(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/credential-types/new")
	orgID := optionValue(t, form, "organization", "acme")
	kind := optionValue(t, form, "kind", "cloud")

	// uniqueName because the registry and its stores are process-wide, so
	// -count=3 runs this body three times against one store. The namespace
	// pattern refuses a hyphen, so it is the same name with underscores.
	name := uniqueName(t, "form-created-type")
	namespace := strings.ReplaceAll(name, "-", "_")

	w := h.post(t, "/ui/credential-types", map[string]string{
		"name":         name,
		"description":  "created through the real form",
		"kind":         kind,
		"namespace":    namespace,
		"organization": orgID,
	})
	if w.Code >= http.StatusBadRequest {
		t.Fatalf("creating a credential type = %d, want a redirect: %s", w.Code, w.Body.String())
	}

	if list := body(t, h, "/ui/credential-types"); !strings.Contains(list, name) {
		t.Fatalf("the type created through the form is not in the list:\n%s", list)
	}
}

func TestCredentialTypesForm_DuplicateNameIsAFieldError(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/credential-types/new")
	orgID := optionValue(t, form, "organization", "acme")
	kind := optionValue(t, form, "kind", "cloud")
	name := uniqueName(t, "dup-type")
	namespace := strings.ReplaceAll(name, "-", "_")

	first := h.post(t, "/ui/credential-types", map[string]string{
		"name": name, "description": "first", "kind": kind,
		"namespace": namespace, "organization": orgID,
	})
	if first.Code >= http.StatusBadRequest {
		t.Fatalf("the first create = %d, want a redirect: %s", first.Code, first.Body.String())
	}

	// A second type with the same name in the same organization. The store
	// refuses it, and the refusal must land on the name control rather than
	// as a 500 or a message about a different field.
	second := h.post(t, "/ui/credential-types", map[string]string{
		"name": name, "description": "second", "kind": kind,
		"namespace": namespace + "_two", "organization": orgID,
	})
	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a duplicate name = %d, want 422 with a field error: %s", second.Code, second.Body.String())
	}
	if b := second.Body.String(); !strings.Contains(b, "already exists") {
		t.Errorf("the duplicate-name refusal does not explain itself:\n%s", b)
	}
}

// TestCredentialTypesForm_MetadataEditKeepsInputsAndInjectors is the
// carry-forward the writer exists to guarantee. The metadata form has no
// control for the inputs schema or the injectors, and UpdateType writes
// both wholesale, so a rename that did not read the stored row first would
// blank them. The list's own summary columns are what would show it.
func TestCredentialTypesForm_MetadataEditKeepsInputsAndInjectors(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}

	// The seeded custom type carries three inputs (two secret) and one env
	// injector. Read the summary the list shows before the edit so the
	// comparison is against what was really there, not a guess.
	before := rowSummary(t, h, id)

	form := h.get(t, "/ui/credential-types/"+id+"/edit")
	if form.Code != http.StatusOK {
		t.Fatalf("GET the edit form = %d, want 200", form.Code)
	}
	name := firstValue(t, form.Body.String(), "name")
	kind := firstValue(t, form.Body.String(), "kind")

	w := h.post(t, "/ui/credential-types/"+id, map[string]string{
		"name":        name,
		"description": "edited, and the schema had better survive",
		"kind":        kind,
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("editing the metadata = %d, want a redirect: %s", w.Code, w.Body.String())
	}

	if after := rowSummary(t, h, id); after != before {
		t.Errorf("a metadata edit changed the inputs or injectors summary:\nbefore %q\nafter  %q", before, after)
	}
}

// rowSummary returns the inputs and injectors cells the list renders for one
// type, which is where a lost schema would show first.
func rowSummary(t *testing.T, h *harness, id string) string {
	t.Helper()
	detail := h.get(t, "/ui/credential-types/"+id).Body.String()
	// The env injector's variable name is the load-bearing token: it is
	// what the seeded type declares and what a wholesale overwrite would
	// erase. Its presence is a sufficient witness that the schema survived.
	return witness(detail, "CONFORMANCE_TOKEN") + "|" + witness(detail, "api_token")
}

func witness(body, token string) string {
	if strings.Contains(body, token) {
		return token
	}
	return "MISSING:" + token
}

// firstValue reads what a rendered control was prefilled with: an input's
// value, or a select's selected option.
func firstValue(t *testing.T, body, name string) string {
	t.Helper()
	values := renderedValues(body, name)
	if len(values) == 0 {
		t.Fatalf("the form rendered no control named %q", name)
	}
	return values[0]
}

// typeIDByName finds the credential type whose detail page carries a name,
// which is how a test recovers the id of something it created through the
// form without the redirect telling it.
func typeIDByName(t *testing.T, h *harness, name string) string {
	t.Helper()
	for _, id := range recordIDs(t, h, "credential-types") {
		if strings.Contains(h.get(t, "/ui/credential-types/"+id).Body.String(), name) {
			return id
		}
	}
	return ""
}

func TestCredentialTypesForm_DeletesACustomType(t *testing.T) {
	h := newHarness(t, adminIdentity)

	form := body(t, h, "/ui/credential-types/new")
	orgID := optionValue(t, form, "organization", "acme")
	kind := optionValue(t, form, "kind", "cloud")
	name := uniqueName(t, "delete-me-type")
	namespace := strings.ReplaceAll(name, "-", "_")

	create := h.post(t, "/ui/credential-types", map[string]string{
		"name": name, "description": "made to be deleted", "kind": kind,
		"namespace": namespace, "organization": orgID,
	})
	if create.Code >= http.StatusBadRequest {
		t.Fatalf("creating the type to delete = %d: %s", create.Code, create.Body.String())
	}
	id := typeIDByName(t, h, name)
	if id == "" {
		t.Fatalf("the created type %q is not listed", name)
	}

	del := h.post(t, "/ui/credential-types/"+id, map[string]string{"_method": "DELETE"})
	if del.Code >= http.StatusBadRequest {
		t.Fatalf("deleting a custom type = %d, want a redirect: %s", del.Code, del.Body.String())
	}
	if list := body(t, h, "/ui/credential-types"); strings.Contains(list, name) {
		t.Errorf("the deleted type is still listed:\n%s", list)
	}
}

func TestCredentialTypes_ManagedTypeWithdrawsEditAndDelete(t *testing.T) {
	h := newHarness(t, adminIdentity)

	managed := firstManagedTypeID(t, h)
	if managed == "" {
		t.Fatal("the fixture ships no managed type, so the withdrawal cannot be observed")
	}
	detail := h.get(t, "/ui/credential-types/"+managed).Body.String()
	if strings.Contains(detail, editLink("credential-types", managed)) {
		t.Error("a managed type's detail offers an Edit control the store would refuse")
	}
	if strings.Contains(detail, confirmDeleteDialog) {
		t.Error("a managed type's detail offers a Delete control the store would refuse")
	}

	// The custom type is the contrast: the same page, both controls
	// present, so the withdrawal above is a real difference rather than the
	// page never rendering either.
	custom := firstEditableRecordID(t, h, "credential-types")
	page := h.get(t, "/ui/credential-types/"+custom).Body.String()
	if !strings.Contains(page, editLink("credential-types", custom)) {
		t.Error("a custom type's detail offers no Edit control")
	}
	if !strings.Contains(page, confirmDeleteDialog) {
		t.Error("a custom type's detail offers no Delete control")
	}
}

// TestCredentialTypes_InputsTabShowsTheSchema proves the Inputs section
// renders a type's own input schema on its detail page, which is what the
// list's summary column can only count.
func TestCredentialTypes_InputsTabShowsTheSchema(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}
	page := h.get(t, "/ui/credential-types/"+id+"?tab="+"inputs").Body.String()

	// The seeded custom type declares api_token (secret) and api_url. Both
	// must appear on the Inputs tab, and the secret one must be marked as
	// such rather than shown like an ordinary field.
	for _, want := range []string{"api_token", "api_url"} {
		if !strings.Contains(page, want) {
			t.Errorf("the Inputs tab does not list the input %q:\n%s", want, page)
		}
	}
}

// TestCredentialTypesForm_AddInputAppendsToTheSchema drives the Add-input
// header action end to end and proves the new field lands on the Inputs tab
// while the type's existing inputs and its injector survive.
func TestCredentialTypesForm_AddInputAppendsToTheSchema(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}

	// The input id pattern refuses a hyphen, so uniqueName's is translated.
	inputID := strings.ReplaceAll(uniqueName(t, "region"), "-", "_")

	w := h.post(t, "/ui/credential-types/"+id+"/add-input", map[string]string{
		"id":    inputID,
		"label": "Region",
		"type":  "string",
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("adding an input = %d, want a redirect: %s", w.Code, w.Body.String())
	}

	page := h.get(t, "/ui/credential-types/"+id+"?tab=inputs").Body.String()
	if !strings.Contains(page, inputID) {
		t.Errorf("the added input %q is not on the Inputs tab:\n%s", inputID, page)
	}
	// The injector the add never touched must still be there.
	if !strings.Contains(h.get(t, "/ui/credential-types/"+id).Body.String(), "CONFORMANCE_TOKEN") {
		t.Error("adding an input blanked the type's injector")
	}
}

// TestCredentialTypesForm_AddInputRejectsABadIdentifier proves the store's
// own refusal reaches the form as a field error rather than a 500.
func TestCredentialTypesForm_AddInputRejectsABadIdentifier(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}

	w := h.post(t, "/ui/credential-types/"+id+"/add-input", map[string]string{
		"id":    "Not A Valid Id",
		"label": "Region",
		"type":  "string",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an invalid input id = %d, want 422 with a field error: %s", w.Code, w.Body.String())
	}
}

// TestCredentialTypesForm_AddInjectorAppendsToTheDocument drives the
// Add-injector action and proves the new environment variable lands on the
// Injectors tab while the type's inputs survive.
func TestCredentialTypesForm_AddInjectorAppendsToTheDocument(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}

	w := h.post(t, "/ui/credential-types/"+id+"/add-injector", map[string]string{
		"target":   "env",
		"name":     "API_URL",
		"template": "{{ api_url }}",
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("adding an injector = %d, want a redirect: %s", w.Code, w.Body.String())
	}

	page := h.get(t, "/ui/credential-types/"+id+"?tab=injectors").Body.String()
	if !strings.Contains(page, "API_URL") {
		t.Errorf("the added injector API_URL is not on the Injectors tab:\n%s", page)
	}
	// The inputs the injector edit never touched must still be there.
	if !strings.Contains(h.get(t, "/ui/credential-types/"+id+"?tab=inputs").Body.String(), "api_token") {
		t.Error("adding an injector blanked the type's input schema")
	}
}

// TestCredentialTypesForm_AddInjectorRejectsADangerousEnvName proves the
// store's refusal of a code-execution environment variable reaches the form
// as a field error rather than a 500. This is the reason authoring an
// injector through a structured, validated control is safe.
func TestCredentialTypesForm_AddInjectorRejectsADangerousEnvName(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}

	w := h.post(t, "/ui/credential-types/"+id+"/add-injector", map[string]string{
		"target":   "env",
		"name":     "LD_PRELOAD",
		"template": "{{ api_token }}",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a code-execution env name = %d, want 422 with a field error: %s", w.Code, w.Body.String())
	}
}

// removeInputForm is the row control the Inputs tab renders for one input,
// and removeInjectorForm the same on the Injectors tab. Both are matched as
// the form's action, because that is the one thing the button, its dialog
// and the route all have to agree about.
func removeInputForm(id, input string) string {
	return `action="/ui/credential-types/` + id + `/remove-input/` + input + `"`
}

func removeInjectorForm(id, row string) string {
	return `action="/ui/credential-types/` + id + `/remove-injector/` + row + `"`
}

// TestCredentialTypes_RemoveInputTakesItOutOfTheSchema is the row half's
// central case on a real resource: an input added through the header control
// can be taken back out through the row one.
//
// The type's injector document is checked afterwards for the same reason the
// add test checks it: set-inputs and set-injectors are two narrowings of one
// store update, and a removal built from the schema alone would blank the
// document beside it.
func TestCredentialTypes_RemoveInputTakesItOutOfTheSchema(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}
	inputID := strings.ReplaceAll(uniqueName(t, "region"), "-", "_")

	added := h.post(t, "/ui/credential-types/"+id+"/add-input", map[string]string{
		"id": inputID, "label": "Region", "type": "string",
	})
	if added.Code != http.StatusSeeOther {
		t.Fatalf("adding the input to remove = %d, want a redirect: %s", added.Code, added.Body.String())
	}

	// The control has to be on the page before it is posted to. A test that
	// only posted would pass against a route with no button above it, which
	// is a feature nobody can reach.
	tab := h.get(t, "/ui/credential-types/"+id+"?tab=inputs").Body.String()
	if !strings.Contains(tab, removeInputForm(id, inputID)) {
		t.Fatalf("the Inputs tab renders no Remove control for %q:\n%s", inputID, tab)
	}

	removed := h.post(t, "/ui/credential-types/"+id+"/remove-input/"+inputID, nil)
	if removed.Code != http.StatusSeeOther {
		t.Fatalf("removing the input = %d, want a redirect: %s", removed.Code, removed.Body.String())
	}

	after := h.get(t, "/ui/credential-types/"+id+"?tab=inputs").Body.String()
	if strings.Contains(after, inputID) {
		t.Errorf("the removed input %q is still on the Inputs tab", inputID)
	}
	if !strings.Contains(h.get(t, "/ui/credential-types/"+id+"?tab=injectors").Body.String(), "CONFORMANCE_TOKEN") {
		t.Error("removing an input blanked the type's injector document")
	}
}

// TestCredentialTypes_RemoveInputAnInjectorNeedsIsRefusedInTheStoresWords is
// the case the whole refusal path was built for.
//
// The fixture's injector renders {{ api_token }}, so removing api_token
// leaves a document referencing an input the type no longer declares, and
// credtype refuses it. What matters is that the person who pressed the
// button is told which rule stopped them, on a page they can act on, rather
// than being shown the words "internal error" while the reason goes to a log
// they cannot read.
func TestCredentialTypes_RemoveInputAnInjectorNeedsIsRefusedInTheStoresWords(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}

	w := h.post(t, "/ui/credential-types/"+id+"/remove-input/api_token", nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("removing an input an injector needs = %d, want 422: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "api_token") {
		t.Errorf("the refusal does not name the input that caused it:\n%s", w.Body.String())
	}

	// Refused means nothing changed, not merely that the response said so.
	if !strings.Contains(h.get(t, "/ui/credential-types/"+id+"?tab=inputs").Body.String(), "api_token") {
		t.Error("the input was removed despite the refusal")
	}
}

// TestCredentialTypes_RemoveInjectorTakesItOutOfTheDocument is the other
// tab's row control, which addresses a row id the section itself invented
// ("env-NAME") rather than one the stored document carries.
func TestCredentialTypes_RemoveInjectorTakesItOutOfTheDocument(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}

	added := h.post(t, "/ui/credential-types/"+id+"/add-injector", map[string]string{
		"target": "env", "name": "API_URL", "template": "{{ api_url }}",
	})
	if added.Code != http.StatusSeeOther {
		t.Fatalf("adding the injector to remove = %d, want a redirect: %s", added.Code, added.Body.String())
	}

	tab := h.get(t, "/ui/credential-types/"+id+"?tab=injectors").Body.String()
	if !strings.Contains(tab, removeInjectorForm(id, "env-API_URL")) {
		t.Fatalf("the Injectors tab renders no Remove control for env-API_URL:\n%s", tab)
	}

	removed := h.post(t, "/ui/credential-types/"+id+"/remove-injector/env-API_URL", nil)
	if removed.Code != http.StatusSeeOther {
		t.Fatalf("removing the injector = %d, want a redirect: %s", removed.Code, removed.Body.String())
	}

	after := h.get(t, "/ui/credential-types/"+id+"?tab=injectors").Body.String()
	if strings.Contains(after, "API_URL") {
		t.Errorf("the removed injector is still on the Injectors tab:\n%s", after)
	}
	// The one the removal never named must survive, or this is a document
	// being replaced rather than an entry being taken out of it.
	if !strings.Contains(after, "CONFORMANCE_TOKEN") {
		t.Error("removing one injector took the others with it")
	}
	if !strings.Contains(h.get(t, "/ui/credential-types/"+id+"?tab=inputs").Body.String(), "api_token") {
		t.Error("removing an injector blanked the type's input schema")
	}
}

// TestCredentialTypes_RemoveRefusesARowThatNamesNothing proves a stale page
// is answered rather than redirected.
//
// A redirect would render the tab again, the row would be absent, and the
// reader would conclude their click worked. On a page left open while
// somebody else edited the type, that reading is false.
func TestCredentialTypes_RemoveRefusesARowThatNamesNothing(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstEditableRecordID(t, h, "credential-types")
	if id == "" {
		t.Fatal("no editable credential type in the fixture")
	}

	cases := map[string]string{
		"an input that is not in the schema":    "/ui/credential-types/" + id + "/remove-input/never_existed",
		"an injector row with no target prefix": "/ui/credential-types/" + id + "/remove-injector/bare",
		"an injector that is not in the document": "/ui/credential-types/" + id +
			"/remove-injector/env-NEVER_EXISTED",
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			if w := h.post(t, target, nil); w.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestCredentialTypes_ManagedTypeWithdrawsTheRemoveControls is the managed
// case one level down from the record's own.
//
// UpdateType refuses a managed type in its second statement, so every schema
// and injector control on one could only ever fail. The record's edit and
// delete were already withdrawn for exactly this reason; the set-inputs and
// set-injectors relations were missing from that predicate, so the add
// controls were being offered on a platform type and answered with a
// refusal, and the remove controls would have inherited it.
func TestCredentialTypes_ManagedTypeWithdrawsTheRemoveControls(t *testing.T) {
	h := newHarness(t, adminIdentity)

	id := firstManagedTypeID(t, h)
	if id == "" {
		t.Skip("no managed credential type in the fixture")
	}

	inputs := h.get(t, "/ui/credential-types/"+id+"?tab=inputs").Body.String()
	if strings.Contains(inputs, "/remove-input/") {
		t.Error("a managed type offers a Remove control on its inputs, which the store would refuse")
	}
	if strings.Contains(inputs, "/add-input") {
		t.Error("a managed type offers Add input, which the store would refuse")
	}

	injectors := h.get(t, "/ui/credential-types/"+id+"?tab=injectors").Body.String()
	if strings.Contains(injectors, "/remove-injector/") {
		t.Error("a managed type offers a Remove control on its injectors, which the store would refuse")
	}
	if strings.Contains(injectors, "/add-injector") {
		t.Error("a managed type offers Add injector, which the store would refuse")
	}
}
