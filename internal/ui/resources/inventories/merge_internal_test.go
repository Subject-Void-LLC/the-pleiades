// This file runs the edit form's merge through the real ent-backed set store,
// on a real SQLite file opened the way the Controller opens one, so the fold
// is held to what UpdateWith really does under its lock: the members it adds
// are checked, the ones it keeps are not, and a device deleted since the form
// opened does not block a save that leaves the list alone.
package inventories

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// openStore opens the real set store over a fresh SQLite file, with one
// organization and devices named by names, and returns their ids in order.
func openStore(t *testing.T, names ...string) (inventory.SetStore, *ent.Client, int, []int) {
	t.Helper()
	ctx := context.Background()
	client, err := ent.OpenEmbedded(ctx, filepath.Join(t.TempDir(), "sets.db"))
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ids := make([]int, 0, len(names))
	for _, name := range names {
		ids = append(ids, client.Device.Create().SetName(name).SetType("linux_server").SetOrganizationID(org.ID).SaveX(ctx).ID)
	}
	return inventory.NewEntSetStore(client), client, org.ID, ids
}

// editOf is an edit submission: what the form showed and what was submitted.
func editOf(shown inventory.Set, submitted inventory.Set) record {
	return record{Set: submitted, opened: &opened{
		name: shown.Name, description: shown.Description,
		groupIDs: shown.GroupIDs, deviceIDs: shown.DeviceIDs,
	}}
}

// TestMergeFold_ADeviceDeletedSinceTheFormOpenedDoesNotBlockTheSave: the form
// showed two devices; one is deleted; the form is saved with a description
// fix and the list untouched. The save succeeds, where replacing the list
// whole named a device that no longer exists.
func TestMergeFold_ADeviceDeletedSinceTheFormOpenedDoesNotBlockTheSave(t *testing.T) {
	t.Parallel()
	store, client, org, devices := openStore(t, "keep", "gone")
	ctx := context.Background()
	shown, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: org, DeviceIDs: devices})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	client.Device.DeleteOneID(devices[1]).ExecX(ctx)

	submitted := shown
	submitted.Description = "typo fixed"
	got, err := store.UpdateWith(ctx, shown.ID, mergeFold(editOf(shown, submitted)))
	if err != nil {
		t.Fatalf("UpdateWith() error = %v, want the save to succeed", err)
	}
	if got.Description != "typo fixed" || !slices.Equal(got.DeviceIDs, devices[:1]) {
		t.Errorf("after the save: %+v, want the fix and only %v", got, devices[:1])
	}
}

// TestMergeFold_ARevocationHoldsAndAnAddIsChecked: the form adds a device
// while another update removes one; the removal holds and the add is applied.
// An add naming a device that does not exist is still refused, so the merge
// cannot reach past the store's member checks.
func TestMergeFold_ARevocationHoldsAndAnAddIsChecked(t *testing.T) {
	t.Parallel()
	store, _, org, devices := openStore(t, "a", "b", "c")
	ctx := context.Background()
	shown, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: org, DeviceIDs: devices[:2]})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.UpdateWith(ctx, shown.ID, func(s inventory.Set) (inventory.Set, error) {
		s.DeviceIDs = devices[:1]
		return s, nil
	}); err != nil {
		t.Fatalf("the revoking update: %v", err)
	}

	submitted := shown
	submitted.DeviceIDs = append(slices.Clone(devices[:2]), devices[2])
	got, err := store.UpdateWith(ctx, shown.ID, mergeFold(editOf(shown, submitted)))
	if err != nil {
		t.Fatalf("UpdateWith() error = %v", err)
	}
	if want := []int{devices[0], devices[2]}; !slices.Equal(got.DeviceIDs, want) {
		t.Errorf("devices = %v, want %v", got.DeviceIDs, want)
	}

	submitted.DeviceIDs = append(submitted.DeviceIDs, 999999)
	if _, err := store.UpdateWith(ctx, shown.ID, mergeFold(editOf(shown, submitted))); !errors.Is(err, inventory.ErrUnknownMember) {
		t.Errorf("an added device that does not exist: error = %v, want ErrUnknownMember", err)
	}
}

// TestMergeFold_RefusesWhatItCannotMerge: a text field both writers changed,
// and a submission missing an original, are field faults naming the field,
// and the stored inventory is left as it was.
func TestMergeFold_RefusesWhatItCannotMerge(t *testing.T) {
	t.Parallel()
	store, _, org, _ := openStore(t)
	ctx := context.Background()
	shown, err := store.Create(ctx, inventory.Set{Name: "edge", Description: "original", OrganizationID: org})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.UpdateWith(ctx, shown.ID, func(s inventory.Set) (inventory.Set, error) {
		s.Description = "theirs"
		return s, nil
	}); err != nil {
		t.Fatalf("the other update: %v", err)
	}

	submitted := shown
	submitted.Description = "mine"
	missing := editOf(shown, shown)
	missing.opened.missing = []string{"devices"}
	cases := map[string]struct {
		edit  record
		field string
	}{
		"both changed the description": {edit: editOf(shown, submitted), field: "description"},
		"an original is missing":       {edit: missing, field: "devices"},
		"no originals at all":          {edit: record{Set: submitted}, field: "name"},
	}
	for name, tc := range cases {
		_, err := store.UpdateWith(ctx, shown.ID, mergeFold(tc.edit))
		var fault view.FieldFault
		if !errors.As(err, &fault) || fault.Field != tc.field {
			t.Errorf("%s: error = %v, want a field fault on %s", name, err, tc.field)
		}
		if got, _ := store.Get(ctx, shown.ID); got.Description != "theirs" {
			t.Errorf("%s: the description became %q", name, got.Description)
		}
	}
}

// TestMergeFold_LineBreaksABrowserRewroteAreNotAChange: a description stored
// with LF through the API comes back from the browser as CRLF, in its control
// and its original alike, and a name holding a line break comes back without
// it. Neither reads as a change made underneath, so the save of another field
// goes through and leaves them as stored.
func TestMergeFold_LineBreaksABrowserRewroteAreNotAChange(t *testing.T) {
	t.Parallel()
	store, _, org, devices := openStore(t, "a")
	ctx := context.Background()
	shown, err := store.Create(ctx, inventory.Set{Name: "edge\nrouters", Description: "line one\nline two", OrganizationID: org})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	posted := record{
		Set:    inventory.Set{Name: "edgerouters", Description: "line one\r\nline two", DeviceIDs: devices},
		opened: &opened{name: "edge\r\nrouters", description: "line one\r\nline two"},
	}
	got, err := store.UpdateWith(ctx, shown.ID, mergeFold(posted))
	if err != nil {
		t.Fatalf("UpdateWith() error = %v, want the device added", err)
	}
	if got.Name != shown.Name || got.Description != shown.Description || !slices.Equal(got.DeviceIDs, devices) {
		t.Errorf("after the save: %+v, want the stored text kept and the device added", got)
	}
}

// TestAsFault_TheStoreRefusalsTheFormShows: a member the store refuses, a
// taken name and a NUL become field errors with fixed text, the member
// refusal the same for an unknown and a foreign member, and anything else is
// returned as it is.
func TestAsFault_TheStoreRefusalsTheFormShows(t *testing.T) {
	cases := map[string]struct {
		err   error
		field string
	}{
		"an unknown member": {err: fmt.Errorf("%w: 1 device(s)", inventory.ErrUnknownMember), field: "devices"},
		"a foreign member":  {err: fmt.Errorf("%w: 1 device(s)", inventory.ErrCrossTenantMember), field: "devices"},
		"a taken name":      {err: inventory.ErrSetExists, field: "name"},
		"a NUL":             {err: &inventory.NULError{Field: "description"}, field: "description"},
	}
	texts := map[string]string{}
	for name, tc := range cases {
		var fault view.FieldFault
		if !errors.As(asFault(tc.err), &fault) || fault.Field != tc.field {
			t.Errorf("%s: asFault() = %v, want a fault on %s", name, asFault(tc.err), tc.field)
		}
		texts[name] = fault.Message
	}
	if texts["an unknown member"] != texts["a foreign member"] {
		t.Error("an unknown and a foreign member answer differently")
	}
	other := errors.New("boom")
	if asFault(other) != other || asFault(nil) != nil {
		t.Error("asFault changed an error it does not map")
	}
}
