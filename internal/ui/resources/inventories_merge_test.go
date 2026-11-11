// Package resources_test's inventory edit merge coverage: an edit form saved
// after someone else changed the inventory keeps their change.
//
// The form used to replace the whole inventory with what it submitted, so an
// operator saving a description fix after another had removed a device put
// the device back, which on a shared inventory re-grants it to every team the
// inventory is shared with. Each test renders the real edit form, changes the
// inventory underneath it as another writer would, and submits what the form
// rendered (renderedSubmission), hidden originals included.
package resources_test

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// renderedSubmission reads what a rendered form submits when its button is
// pressed: every hidden original, every text and number input, every textarea
// and every selected option of every select, repeated per selection. The CSRF
// token is left to the harness's post, which adds its own.
//
// A small reader over this application's own markup, as renderedFormValues is
// (credentialtypes_test.go), and for the same reason: a hand-built body posts
// the right values whether or not the form rendered them, so a test of the
// round trip would pass with the hidden originals deleted.
func renderedSubmission(tb testing.TB, body string) url.Values {
	tb.Helper()
	out := url.Values{}
	for _, m := range regexp.MustCompile(`<input type="hidden" name="(_original\.[^"]+)" value="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		out.Add(m[1], html.UnescapeString(m[2]))
	}
	for _, m := range regexp.MustCompile(`<input type="(?:text|number)" id="f-[^"]*" name="([^"]+)" value="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		out.Set(m[1], html.UnescapeString(m[2]))
	}
	for _, m := range regexp.MustCompile(`(?s)<textarea id="f-[^"]*" name="([^"]+)"[^>]*>(.*?)</textarea>`).FindAllStringSubmatch(body, -1) {
		out.Set(m[1], html.UnescapeString(m[2]))
	}
	for _, m := range regexp.MustCompile(`(?s)<select id="f-[^"]*" name="([^"]+)".*?</select>`).FindAllStringSubmatch(body, -1) {
		for _, chosen := range regexp.MustCompile(`<option value="([^"]*)" selected>`).FindAllStringSubmatch(m[0], -1) {
			out.Add(m[1], html.UnescapeString(chosen[1]))
		}
	}
	return out
}

// openEdit renders an inventory's edit form and returns what it submits.
func openEdit(t *testing.T, h *harness, id int) url.Values {
	t.Helper()
	form := renderedSubmission(t, body(t, h, fmt.Sprintf("/ui/inventories/%d/edit", id)))
	if len(form["_original.name"]) != 1 {
		t.Fatalf("the edit form rendered no original for the name: %v", form)
	}
	return form
}

// seedInventory stores an inventory of its own for one test, so no test
// depends on what another left in the shared fake store.
func seedInventory(t *testing.T, set inventory.Set) int {
	t.Helper()
	set.OrganizationID, set.OrganizationName = 1, "conformance"
	created, err := conformanceSets.Create(context.Background(), set)
	if err != nil {
		t.Fatalf("seeding an inventory: %v", err)
	}
	return created.ID
}

// underneath changes the stored inventory as another writer would, between
// the form being opened and being saved.
func underneath(t *testing.T, id int, change func(*inventory.Set)) {
	t.Helper()
	if _, err := conformanceSets.UpdateWith(context.Background(), id, func(s inventory.Set) (inventory.Set, error) {
		change(&s)
		return s, nil
	}); err != nil {
		t.Fatalf("changing the inventory underneath the form: %v", err)
	}
}

// stored reads an inventory back from the store.
func stored(t *testing.T, id int) inventory.Set {
	t.Helper()
	set, err := conformanceSets.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("reading inventory %d: %v", id, err)
	}
	return set
}

// save posts form to the inventory and returns the response.
func save(t *testing.T, h *harness, id int, form url.Values) (int, string) {
	t.Helper()
	w := h.postValues(t, fmt.Sprintf("/ui/inventories/%d", id), form)
	return w.Code, w.Body.String()
}

// TestUIEdit_UntouchedMembersDoNotRevertARevocation is the defect: a device
// removed after the form opened stays removed when the form, which never
// touched the list, is saved with a description fix.
func TestUIEdit_UntouchedMembersDoNotRevertARevocation(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := seedInventory(t, inventory.Set{Name: uniqueName(t, "revoke"), DeviceIDs: []int{11, 12}})
	form := openEdit(t, h, id)

	underneath(t, id, func(s *inventory.Set) { s.DeviceIDs = []int{11} })
	form.Set("description", "typo fixed")
	if status, page := save(t, h, id, form); status >= http.StatusBadRequest {
		t.Fatalf("saving the description = %d: %s", status, page)
	}
	got := stored(t, id)
	if !slices.Equal(got.DeviceIDs, []int{11}) || got.Description != "typo fixed" {
		t.Errorf("after the save: devices %v description %q, want [11] and the fix", got.DeviceIDs, got.Description)
	}
}

// TestUIEdit_AnAddDuringARevocationMergesByElement: the form adds device 13
// while another writer removes 12. Both changes hold.
func TestUIEdit_AnAddDuringARevocationMergesByElement(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := seedInventory(t, inventory.Set{Name: uniqueName(t, "merge"), DeviceIDs: []int{11, 12}})
	form := openEdit(t, h, id)

	underneath(t, id, func(s *inventory.Set) { s.DeviceIDs = []int{11} })
	form.Add("devices", "13")
	if status, page := save(t, h, id, form); status >= http.StatusBadRequest {
		t.Fatalf("adding a device = %d: %s", status, page)
	}
	if got := stored(t, id).DeviceIDs; !slices.Equal(got, []int{11, 13}) {
		t.Errorf("devices = %v, want [11 13]", got)
	}
}

// TestUIEdit_TwoTabsBothSave: two forms opened on one inventory, one renaming
// it and one editing its description, both save.
func TestUIEdit_TwoTabsBothSave(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := seedInventory(t, inventory.Set{Name: uniqueName(t, "tabs"), Description: "before"})
	first, second := openEdit(t, h, id), openEdit(t, h, id)
	renamed := uniqueName(t, "renamed")

	first.Set("name", renamed)
	if status, page := save(t, h, id, first); status >= http.StatusBadRequest {
		t.Fatalf("the first tab's rename = %d: %s", status, page)
	}
	second.Set("description", "after")
	if status, page := save(t, h, id, second); status >= http.StatusBadRequest {
		t.Fatalf("the second tab's description = %d: %s", status, page)
	}
	if got := stored(t, id); got.Name != renamed || got.Description != "after" {
		t.Errorf("after both tabs: name %q description %q, want %q and \"after\"", got.Name, got.Description, renamed)
	}
}

// TestUIEdit_AFieldChangedUnderneathIsRefusedAndStaysRefused: the form and
// another writer both change the description. The save is refused with a
// field error that names neither value, the stored one is kept, and
// submitting the redisplayed form again is refused again: a plain resubmit
// must not overwrite the other writer's change. The redisplay keeps every
// device selection, where it used to keep only the first.
func TestUIEdit_AFieldChangedUnderneathIsRefusedAndStaysRefused(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := seedInventory(t, inventory.Set{Name: uniqueName(t, "scalar"), Description: "original", DeviceIDs: []int{11, 12}})
	form := openEdit(t, h, id)

	underneath(t, id, func(s *inventory.Set) { s.Description = "theirs-canary" })
	form.Set("description", "mine-canary")
	status, page := save(t, h, id, form)
	if status != http.StatusUnprocessableEntity || !strings.Contains(page, "Someone else changed this") {
		t.Fatalf("the conflicting save = %d, want 422 with the field error: %s", status, page)
	}
	if strings.Contains(page, "theirs-canary") {
		t.Error("the refusal shows the other writer's value")
	}
	if got := stored(t, id).Description; got != "theirs-canary" {
		t.Errorf("the description became %q, want the other writer's kept", got)
	}

	again := renderedSubmission(t, page)
	// Every selection comes back, not only the first: a redisplay that kept
	// one would deselect the rest on the next save.
	if got := again["devices"]; !slices.Equal(got, []string{"11", "12"}) {
		t.Errorf("the redisplay selects devices %v, want [11 12]", got)
	}
	if got := again.Get("_original.description"); got != "original" {
		t.Errorf("the redisplay carries original %q, want the one it was opened with", got)
	}
	if status, _ := save(t, h, id, again); status != http.StatusUnprocessableEntity {
		t.Errorf("submitting the redisplayed form again = %d, want 422", status)
	}
	if got := stored(t, id).Description; got != "theirs-canary" {
		t.Errorf("the resubmit overwrote the description with %q", got)
	}
}

// TestUIEdit_AMemberTheChooserCannotShowSurvives: a device the chooser does
// not offer (past its bound, or added through the API) is not in the form's
// original, so saving the form leaves it in the inventory.
func TestUIEdit_AMemberTheChooserCannotShowSurvives(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := seedInventory(t, inventory.Set{Name: uniqueName(t, "bound"), DeviceIDs: []int{11, 99}})
	form := openEdit(t, h, id)
	if got := form.Get("_original.devices"); got != "11" {
		t.Errorf("the original devices = %q, want only the offered 11", got)
	}

	form.Set("description", "edited")
	if status, page := save(t, h, id, form); status >= http.StatusBadRequest {
		t.Fatalf("saving = %d: %s", status, page)
	}
	if got := stored(t, id).DeviceIDs; !slices.Equal(got, []int{11, 99}) {
		t.Errorf("devices = %v, want [11 99]: the unlisted device was dropped", got)
	}
}

// TestUIEdit_AStoredLongNameDoesNotBlockAnotherEdit: a name past the form's
// limit, stored through the API, is not checked by an edit that leaves it as
// it is.
func TestUIEdit_AStoredLongNameDoesNotBlockAnotherEdit(t *testing.T) {
	h := newHarness(t, adminIdentity)
	long := uniqueName(t, strings.Repeat("n", 300))
	id := seedInventory(t, inventory.Set{Name: long})
	form := openEdit(t, h, id)

	form.Set("description", "edited")
	if status, page := save(t, h, id, form); status >= http.StatusBadRequest {
		t.Fatalf("saving the description beside a long stored name = %d: %s", status, page)
	}
	if got := stored(t, id); got.Name != long || got.Description != "edited" {
		t.Errorf("after the save: name kept %v description %q", got.Name == long, got.Description)
	}

	form = openEdit(t, h, id)
	form.Set("name", long+"x")
	if status, _ := save(t, h, id, form); status != http.StatusUnprocessableEntity {
		t.Errorf("changing the long name to a longer one = %d, want 422 for the limit", status)
	}
}

// TestUIEdit_ASubmissionWithoutOriginalsIsRefused: a submission that does not
// say what the form showed cannot be merged, so it is refused and changes
// nothing rather than replacing the inventory whole.
func TestUIEdit_ASubmissionWithoutOriginalsIsRefused(t *testing.T) {
	h := newHarness(t, adminIdentity)
	name := uniqueName(t, "bare")
	id := seedInventory(t, inventory.Set{Name: name, DeviceIDs: []int{11}})

	form := url.Values{}
	form.Set("name", name)
	form.Add("devices", "12")
	status, page := save(t, h, id, form)
	if status != http.StatusUnprocessableEntity || !strings.Contains(page, "does not say what it was opened with") {
		t.Fatalf("a save with no originals = %d, want 422 with the refusal: %s", status, page)
	}
	if got := stored(t, id).DeviceIDs; !slices.Equal(got, []int{11}) {
		t.Errorf("devices = %v, want [11] unchanged", got)
	}

	// An original for a field the form does not keep one for is undeclared.
	form = openEdit(t, h, id)
	form.Set("_original.owner", "mallory")
	if status, _ := save(t, h, id, form); status != http.StatusBadRequest {
		t.Errorf("an original for a field that keeps none = %d, want 400", status)
	}
}
