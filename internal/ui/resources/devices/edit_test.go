// Package devices_test: editing a device through the web UI changes the
// stored device, keeps what the form cannot change, and still refuses an
// edit that lost a race (FAILURE_PATTERNS 1044).
package devices_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// createThroughUI adds a device through the create form and returns it as
// the repository stored it.
func createThroughUI(t *testing.T, h *harness, repo inventory.Repository, name string) pkginventory.InventoryItem {
	t.Helper()
	rec := h.post(t, "/ui/devices", url.Values{
		"name": {name}, "type": {"linux_server"}, "tags": {"edge, critical"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /ui/devices = %d, want 303; body:\n%s", rec.Code, rec.Body.String())
	}
	stored, err := repo.GetByName(context.Background(), name)
	if err != nil {
		t.Fatalf("GetByName(%q) after create = %v, want nil", name, err)
	}
	return stored
}

// editTags opens a device's edit form, submits what it renders with the tags
// replaced, and returns the response.
func editTags(t *testing.T, h *harness, name, tags string) int {
	t.Helper()
	form := h.get(t, "/ui/devices/"+name+"/edit")
	if form.Code != http.StatusOK {
		t.Fatalf("GET edit form = %d, want 200", form.Code)
	}
	values := formFields(t, form.Body.String())
	if _, ok := values["tags"]; !ok {
		t.Fatalf("the edit form renders no tags control; submitted %v", values)
	}
	values.Set("tags", tags)
	rec := h.post(t, "/ui/devices/"+name, values)
	if rec.Code != http.StatusSeeOther {
		t.Logf("POST /ui/devices/%s = %d; body:\n%s", name, rec.Code, rec.Body.String())
	}
	return rec.Code
}

// assertEdited checks that an edit changed the stored device's tags and kept
// everything the form cannot change: its id, its lifecycle state, its source
// and its history, with the version moved on by exactly the one change.
func assertEdited(t *testing.T, repo inventory.Repository, before pkginventory.InventoryItem, wantTags []pkginventory.Tag) {
	t.Helper()
	after, err := repo.GetByName(context.Background(), before.Name())
	if err != nil {
		t.Fatalf("GetByName(%q) after edit = %v, want nil", before.Name(), err)
	}
	if got := after.Tags(); !slices.Equal(got, wantTags) {
		t.Errorf("stored tags = %v, want %v: the edit did not reach the store", got, wantTags)
	}
	if after.ID() != before.ID() {
		t.Errorf("stored id = %q, want %q kept", after.ID(), before.ID())
	}
	if after.State() != before.State() {
		t.Errorf("stored state = %v, want %v kept", after.State(), before.State())
	}
	if after.Source() != before.Source() {
		t.Errorf("stored source = %+v, want %+v kept", after.Source(), before.Source())
	}
	if after.Version() != before.Version()+1 {
		t.Errorf("stored version = %d, want %d (one change past %d)", after.Version(), before.Version()+1, before.Version())
	}
	history := after.History()
	if len(history) != len(before.History())+1 {
		t.Fatalf("history has %d revisions, want %d: the earlier ones kept and one added", len(history), len(before.History())+1)
	}
	last := history[len(history)-1]
	if last.Field != record.RevisionFieldTags || last.Version != after.Version() {
		t.Errorf("last revision = %+v, want a %q change at version %d", last, record.RevisionFieldTags, after.Version())
	}
}

// TestEditingADeviceUpdatesTheStoredRecord is FAILURE_PATTERNS 1044 through
// the real path: the UI handler, the devices view and the Controller's ent
// repository over SQLite.
func TestEditingADeviceUpdatesTheStoredRecord(t *testing.T) {
	repo := newEntRepo(t)
	h := newHarness(t, repo)
	before := createThroughUI(t, h, repo, "edge-router-01")

	if code := editTags(t, h, "edge-router-01", "edge, core"); code != http.StatusSeeOther {
		t.Fatalf("submitting the edit form = %d, want 303", code)
	}
	assertEdited(t, repo, before, []pkginventory.Tag{"edge", "core"})
}

// TestEditingADeviceUpdatesTheFileStore is the same edit over the Crawl
// tier's file repository. No composition root puts the UI on it today, but
// the view is written against the port, so the edit must hold on both.
func TestEditingADeviceUpdatesTheFileStore(t *testing.T) {
	repo, _ := newFileRepo(t)
	h := newHarness(t, repo)
	before := createThroughUI(t, h, repo, "edge-router-02")

	if code := editTags(t, h, "edge-router-02", "edge, core"); code != http.StatusSeeOther {
		t.Fatalf("submitting the edit form = %d, want 303", code)
	}
	assertEdited(t, repo, before, []pkginventory.Tag{"edge", "core"})
}

// TestEditingADeviceKeepsItsPropertiesAndState edits the tags of a device
// that carries what the form cannot show: a property and a lifecycle state
// other than the one a new device starts in. An edit that rebuilt the device
// from the form would store it active with no properties; the edit must
// change the tags and leave both as they were.
func TestEditingADeviceKeepsItsPropertiesAndState(t *testing.T) {
	repo := newEntRepo(t)
	h := newHarness(t, repo)
	ctx := context.Background()
	seeded, err := inventory.NewItemFactory().Build(record.Record{
		ID:         "01890000-0000-7000-8000-0000000000aa",
		Name:       "quarantined-01",
		Type:       "linux_server",
		Properties: map[string]pkginventory.PropertyValue{"host": "10.0.0.9"},
		Tags:       []pkginventory.Tag{"edge"},
		State:      pkginventory.StateQuarantined,
		Source:     pkginventory.SourceAuthority{Plugin: "sync"},
	})
	if err != nil {
		t.Fatalf("building the seeded device: %v", err)
	}
	if err := repo.Create(ctx, seeded); err != nil {
		t.Fatalf("seeding the device: %v", err)
	}
	before, err := repo.GetByName(ctx, "quarantined-01")
	if err != nil {
		t.Fatalf("GetByName after seeding = %v, want nil", err)
	}

	if code := editTags(t, h, "quarantined-01", "edge, core"); code != http.StatusSeeOther {
		t.Fatalf("submitting the edit form = %d, want 303", code)
	}
	assertEdited(t, repo, before, []pkginventory.Tag{"edge", "core"})
	after, err := repo.GetByName(ctx, "quarantined-01")
	if err != nil {
		t.Fatalf("GetByName after the edit = %v, want nil", err)
	}
	if got := after.Properties().Raw()["host"]; got != "10.0.0.9" {
		t.Errorf("stored property host = %v, want 10.0.0.9 kept", got)
	}
	if after.State() != pkginventory.StateQuarantined {
		t.Errorf("stored state = %v, want quarantined kept", after.State())
	}
}

// racingRepo is a real repository with one competing writer: on the first
// Save it is asked for, it first changes the same device's tags through the
// same store and saves that, so the UI's save arrives carrying the version
// the competitor has already moved past. Every write reaches the real store,
// and the conflict is the store's own.
type racingRepo struct {
	inventory.Repository
	t     *testing.T
	raced bool
}

// Save lets the competitor win once, then forwards the caller's save.
func (r *racingRepo) Save(ctx context.Context, item pkginventory.InventoryItem) error {
	if !r.raced {
		r.raced = true
		competitor, err := r.Repository.GetByName(ctx, item.Name())
		if err != nil {
			r.t.Fatalf("the competing writer's read = %v, want nil", err)
		}
		changer, ok := competitor.(interface {
			ChangeTags([]pkginventory.Tag) bool
		})
		if !ok || !changer.ChangeTags([]pkginventory.Tag{"theirs"}) {
			r.t.Fatal("the competing writer could not change the tags")
		}
		if err := r.Repository.Save(ctx, competitor); err != nil {
			r.t.Fatalf("the competing writer's save = %v, want nil", err)
		}
	}
	return r.Repository.Save(ctx, item)
}

// TestAnEditThatLosesARaceIsRefusedAndKeepsTheOtherWrite proves the fix kept
// the version check: an edit computed against a version another writer has
// since moved is refused on the form, and the other writer's change stands.
func TestAnEditThatLosesARaceIsRefusedAndKeepsTheOtherWrite(t *testing.T) {
	store := newEntRepo(t)
	racing := &racingRepo{Repository: store, t: t}
	h := newHarness(t, racing)
	before := createThroughUI(t, h, store, "edge-router-03")

	form := h.get(t, "/ui/devices/edge-router-03/edit")
	values := formFields(t, form.Body.String())
	values.Set("tags", "mine")
	rec := h.post(t, "/ui/devices/edge-router-03", values)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an edit that lost the race = %d, want 422 with the form redisplayed", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Someone else changed this device") {
		t.Errorf("the refused edit does not say why; body:\n%s", body)
	}
	if !strings.Contains(rec.Body.String(), `value="mine"`) {
		t.Errorf("the refused edit lost what the person typed")
	}

	after, err := store.GetByName(context.Background(), "edge-router-03")
	if err != nil {
		t.Fatalf("GetByName after the race = %v, want nil", err)
	}
	if got := after.Tags(); !slices.Equal(got, []pkginventory.Tag{"theirs"}) {
		t.Errorf("stored tags = %v, want the competitor's [theirs] kept", got)
	}
	if after.Version() != before.Version()+1 {
		t.Errorf("stored version = %d, want %d: only the competitor's write landed", after.Version(), before.Version()+1)
	}
}

// TestAnEditFormOffersOnlyWhatAnEditCanChange holds the form to the write:
// the name and type are fixed once a device exists, so the edit form renders
// no control for either, and a submission carrying one is refused rather
// than taken and ignored.
func TestAnEditFormOffersOnlyWhatAnEditCanChange(t *testing.T) {
	repo := newEntRepo(t)
	h := newHarness(t, repo)
	before := createThroughUI(t, h, repo, "edge-router-04")

	form := h.get(t, "/ui/devices/edge-router-04/edit")
	controls := []string{}
	for name := range formFields(t, form.Body.String()) {
		// The form's own reserved keys (where to return to) are not
		// controls a person fills in.
		if !strings.HasPrefix(name, "_") {
			controls = append(controls, name)
		}
	}
	if !slices.Equal(controls, []string{"tags"}) {
		t.Errorf("the edit form renders the controls %v, want the tags alone", controls)
	}

	for _, field := range []string{"name", "type"} {
		smuggled := url.Values{"tags": {"edge"}, field: {"renamed"}}
		if rec := h.post(t, "/ui/devices/edge-router-04", smuggled); rec.Code == http.StatusSeeOther {
			t.Errorf("an edit carrying %s = 303, want it refused", field)
		}
	}
	after, err := repo.GetByName(context.Background(), "edge-router-04")
	if err != nil {
		t.Fatalf("GetByName = %v, want nil", err)
	}
	if after.Version() != before.Version() {
		t.Errorf("stored version = %d, want %d: a refused edit stores nothing", after.Version(), before.Version())
	}
}

// TestAnUnchangedEditStoresNothing submits the edit form as rendered: the
// tags are what is stored, so the edit succeeds and records no revision.
func TestAnUnchangedEditStoresNothing(t *testing.T) {
	repo := newEntRepo(t)
	h := newHarness(t, repo)
	before := createThroughUI(t, h, repo, "edge-router-05")

	if code := editTags(t, h, "edge-router-05", "critical, edge"); code != http.StatusSeeOther {
		t.Fatalf("submitting the unchanged form = %d, want 303", code)
	}
	after, err := repo.GetByName(context.Background(), "edge-router-05")
	if err != nil {
		t.Fatalf("GetByName = %v, want nil", err)
	}
	if after.Version() != before.Version() || len(after.History()) != len(before.History()) {
		t.Errorf("an unchanged edit moved the version to %d with %d revisions, want %d with %d",
			after.Version(), len(after.History()), before.Version(), len(before.History()))
	}
}

// TestEditingAMissingDeviceFails is the edit of a device that is not stored:
// the writer reads before it writes, so nothing is created in its place.
func TestEditingAMissingDeviceFails(t *testing.T) {
	repo := newEntRepo(t)
	h := newHarness(t, repo)
	if rec := h.post(t, "/ui/devices/nowhere", url.Values{"tags": {"edge"}}); rec.Code == http.StatusSeeOther {
		t.Fatalf("editing a device that is not stored = 303, want a failure")
	}
	if _, err := repo.GetByName(context.Background(), "nowhere"); !errors.Is(err, inventory.ErrItemNotFound) {
		t.Errorf("GetByName(nowhere) = %v, want ErrItemNotFound: an edit must not create", err)
	}
}
