package inventory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// This file extends the Phase W4 conformance suite to Repository.Create,
// the write path added so a sync plugin can onboard a device it has never
// seen. It reuses repositoryBackends() from repository_conformance_test.go,
// so a third adapter behind this port inherits every assertion below by
// adding one entry to that list.
//
// Create is worth conformance-testing rather than unit-testing per adapter
// precisely because the two implementations are so different: one inserts a
// row and lets a database constraint enforce uniqueness, the other appends
// to a YAML document and a sidecar state file and has to enforce
// uniqueness itself. Those are exactly the circumstances under which two
// adapters quietly stop agreeing.

// newConformanceItem builds a device to create, distinct from whatever the
// backend was seeded with.
func newConformanceItem(t *testing.T, name string, props map[string]interface{}) pkginventory.InventoryItem {
	t.Helper()

	item, err := inventory.NewItemFactory().Build(record.Record{
		ID:         pkginventory.DeviceID("created-" + name),
		Name:       name,
		Type:       "linux_server",
		Properties: props,
		Tags:       []pkginventory.Tag{"created"},
		State:      pkginventory.StateActive,
		Source:     pkginventory.SourceAuthority{Plugin: "conformance"},
	})
	if err != nil {
		t.Fatalf("building conformance item %q: %v", name, err)
	}
	return item
}

// TestRepositoryConformance_CreateThenGetByName proves a created device is
// immediately readable through the same port, with the properties, tags,
// state, and provenance it was created with intact.
func TestRepositoryConformance_CreateThenGetByName(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			ctx := context.Background()

			item := newConformanceItem(t, "created-host", map[string]interface{}{
				"host": "10.0.0.42",
			})
			if err := repo.Create(ctx, item); err != nil {
				t.Fatalf("Create: %v", err)
			}

			got, err := repo.GetByName(ctx, "created-host")
			if err != nil {
				t.Fatalf("GetByName after Create: %v", err)
			}
			if got.ID() != item.ID() {
				t.Errorf("ID() = %q, want %q", got.ID(), item.ID())
			}
			if host, _ := got.Properties().String("host"); host != "10.0.0.42" {
				t.Errorf("host property = %q, want 10.0.0.42", host)
			}
			if got.Source().Plugin != "conformance" {
				t.Errorf("Source().Plugin = %q, want conformance", got.Source().Plugin)
			}
			if !got.State().CanExecute() {
				t.Errorf("State() = %v, want an executable state", got.State())
			}
			if len(got.Tags()) != 1 || got.Tags()[0] != "created" {
				t.Errorf("Tags() = %v, want [created]", got.Tags())
			}
		})
	}
}

// TestRepositoryConformance_CreateRejectsDuplicateName proves Create never
// overwrites. A sync plugin that rediscovers a device it already onboarded
// must reconcile through Save, so the existing version token and audit
// trail survive rather than being reset by a second insert.
func TestRepositoryConformance_CreateRejectsDuplicateName(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			ctx := context.Background()

			// conformanceHostName is what every backend is seeded with, so
			// creating it again is a duplicate on both adapters.
			dup := newConformanceItem(t, conformanceHostName, map[string]interface{}{
				"host": "10.0.0.99",
			})

			err := repo.Create(ctx, dup)
			if err == nil {
				t.Fatal("expected creating a duplicate name to fail")
			}
			if !errors.Is(err, inventory.ErrItemExists) {
				t.Fatalf("Create duplicate = %v, want it to wrap ErrItemExists", err)
			}

			// The original must be untouched, not partially overwritten.
			got, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName after refused duplicate: %v", err)
			}
			if host, _ := got.Properties().String("host"); host != "10.0.0.9" {
				t.Errorf("host property = %q, want the original 10.0.0.9", host)
			}
		})
	}
}

// TestRepositoryConformance_CreateRejectsUntypedItem proves an item that
// cannot report a device type is refused rather than stored. The ent
// schema's type column is immutable, so a row created without one could
// never be repaired by a later write and nothing could ever hydrate it.
func TestRepositoryConformance_CreateRejectsUntypedItem(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)

			// Build through a Record with no Type. The factory refuses to
			// build one at all, which is the earlier of the two guards; this
			// test pins that the refusal happens somewhere before storage,
			// not that it happens in a particular place.
			_, err := inventory.NewItemFactory().Build(record.Record{
				ID:    "untyped-1",
				Name:  "untyped-host",
				State: pkginventory.StateActive,
			})
			if err == nil {
				t.Fatal("expected building an item with no device type to fail")
			}

			// And nothing was stored as a side effect of trying.
			if _, err := repo.GetByName(context.Background(), "untyped-host"); !errors.Is(err, inventory.ErrItemNotFound) {
				t.Errorf("GetByName(untyped-host) = %v, want ErrItemNotFound", err)
			}
		})
	}
}

// TestRepositoryConformance_GetByNameReportsNotFound proves both adapters
// answer "no such device" with the same sentinel. Without it, only one
// adapter would be distinguishable and reconciliation would treat a broken
// backend as a fleet of brand new devices.
func TestRepositoryConformance_GetByNameReportsNotFound(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)

			_, err := repo.GetByName(context.Background(), "no-such-host")
			if !errors.Is(err, inventory.ErrItemNotFound) {
				t.Fatalf("GetByName(missing) = %v, want it to wrap ErrItemNotFound", err)
			}
		})
	}
}

// TestRepositoryConformance_CreateRejectsItemWithoutDeviceType proves an
// item that satisfies the public InventoryItem contract but cannot report
// the registry key it was built through is refused by Create itself, not
// merely by the factory upstream of it.
//
// inventorytest.Stub is exactly such an item: it implements every method
// of the SDK contract and none of the persistence-side accessors, which is
// the shape any third-party device type could legitimately have. Storing
// one would produce a row with no type, which nothing can ever hydrate.
func TestRepositoryConformance_CreateRejectsItemWithoutDeviceType(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)

			stub := &inventorytest.Stub{
				StubID:    "stub-1",
				StubName:  "stub-host",
				StubState: pkginventory.StateActive,
			}

			err := repo.Create(context.Background(), stub)
			if err == nil {
				t.Fatal("expected Create to refuse an item that reports no device type")
			}
			if !strings.Contains(err.Error(), "device type") {
				t.Errorf("Create = %q, want it to explain the missing device type", err)
			}
		})
	}
}

// TestRepositoryConformance_CreatePersistsHistory proves a created device
// carrying an audit trail keeps it. A plugin replaying a device it
// previously exported arrives with history already attached, and the audit
// trail is the one thing that cannot be reconstructed later if it is
// dropped on insert.
func TestRepositoryConformance_CreatePersistsHistory(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			ctx := context.Background()

			changedAt := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
			item, err := inventory.NewItemFactory().Build(record.Record{
				ID:         "replayed-1",
				Name:       "replayed-host",
				Type:       "linux_server",
				Properties: map[string]interface{}{"host": "10.0.0.44"},
				State:      pkginventory.StateActive,
				Source:     pkginventory.SourceAuthority{Plugin: "conformance"},
				Version:    2,
				History: []pkginventory.Revision{
					{Version: 1, ChangedAt: changedAt, Field: "host", NewValue: "10.0.0.1"},
					{Version: 2, ChangedAt: changedAt, Field: "host", OldValue: "10.0.0.1", NewValue: "10.0.0.44"},
				},
			})
			if err != nil {
				t.Fatalf("building replayed item: %v", err)
			}

			if err := repo.Create(ctx, item); err != nil {
				t.Fatalf("Create: %v", err)
			}

			got, err := repo.GetByName(ctx, "replayed-host")
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if len(got.History()) != 2 {
				t.Fatalf("History() has %d entries, want 2: %+v", len(got.History()), got.History())
			}
			if got.Version() != 2 {
				t.Errorf("Version() = %d, want 2", got.Version())
			}
			last := got.History()[1]
			if last.Field != "host" || last.OldValue != "10.0.0.1" || last.NewValue != "10.0.0.44" {
				t.Errorf("last revision = %+v, want the host 10.0.0.1 to 10.0.0.44 change", last)
			}
		})
	}
}

// TestRepositoryConformance_CreatedItemIsSaveable proves the version token
// a created device carries is a real one: the device can be mutated and
// saved immediately, without an intervening reload.
func TestRepositoryConformance_CreatedItemIsSaveable(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			ctx := context.Background()

			if err := repo.Create(ctx, newConformanceItem(t, "saveable-host", map[string]interface{}{
				"host": "10.0.0.43",
			})); err != nil {
				t.Fatalf("Create: %v", err)
			}

			loaded, err := repo.GetByName(ctx, "saveable-host")
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if err := loaded.AddInfo("role", "database", false); err != nil {
				t.Fatalf("AddInfo: %v", err)
			}
			if err := repo.Save(ctx, loaded); err != nil {
				t.Fatalf("Save after Create: %v", err)
			}

			reloaded, err := repo.GetByName(ctx, "saveable-host")
			if err != nil {
				t.Fatalf("GetByName after Save: %v", err)
			}
			if role, _ := reloaded.Properties().String("role"); role != "database" {
				t.Errorf("role property = %q, want database", role)
			}
			if reloaded.Version() == 0 {
				t.Error("expected the saved device's version to have moved off zero")
			}
		})
	}
}
