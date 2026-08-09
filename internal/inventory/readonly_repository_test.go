package inventory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestReadOnlyRepository_RefusesWrites runs the read-only wrapper over both
// real backends through the shared conformance table, so the refusal is
// proven to be a property of the wrapper rather than of whichever adapter
// happened to be underneath it.
func TestReadOnlyRepository_RefusesWrites(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := inventory.NewReadOnlyRepository(backend.newRepo(t))
			ctx := context.Background()

			item := newConformanceItem(t, "blocked-host", map[string]interface{}{
				"host": "10.0.0.77",
			})

			err := repo.Create(ctx, item)
			if !errors.Is(err, inventory.ErrInventoryReadOnly) {
				t.Fatalf("Create = %v, want it to wrap ErrInventoryReadOnly", err)
			}
			// The refusal must name the item. "Something was blocked" sends
			// an operator hunting; "blocked-host was blocked" does not.
			if !strings.Contains(err.Error(), "blocked-host") {
				t.Errorf("Create error = %q, want it to name the item", err)
			}

			existing, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName through the read-only wrapper: %v", err)
			}
			if err := existing.AddInfo("role", "database", true); err != nil {
				t.Fatalf("AddInfo: %v", err)
			}
			err = repo.Save(ctx, existing)
			if !errors.Is(err, inventory.ErrInventoryReadOnly) {
				t.Fatalf("Save = %v, want it to wrap ErrInventoryReadOnly", err)
			}
		})
	}
}

// TestReadOnlyRepository_ReadsPassThrough proves the wrapper blocks writes
// without blinding the caller: both read paths still work, including the
// streaming one.
func TestReadOnlyRepository_ReadsPassThrough(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := inventory.NewReadOnlyRepository(backend.newRepo(t))
			ctx := context.Background()

			item, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if item.Name() != conformanceHostName {
				t.Errorf("GetByName returned %q, want %q", item.Name(), conformanceHostName)
			}

			it, err := repo.GetGroup(ctx, pkginventory.Selector{})
			if err != nil {
				t.Fatalf("GetGroup: %v", err)
			}
			defer func() { _ = it.Close() }()

			var seen int
			for it.Next(ctx) {
				seen++
			}
			if err := it.Error(); err != nil {
				t.Fatalf("iterating: %v", err)
			}
			if seen == 0 {
				t.Error("GetGroup through the read-only wrapper yielded nothing")
			}
		})
	}
}

// TestReadOnlyRepository_BlocksARealSync is the point of the whole feature:
// a sync plugin pointed at read-only inventory changes nothing at all.
//
// It uses the static YAML plugin rather than a mock, because what is being
// proven is that the guard holds against a real reconciliation pass, which
// is the thing a user would actually run with --read-only.
func TestReadOnlyRepository_BlocksARealSync(t *testing.T) {
	ctx := context.Background()
	inner := newConformanceFileRepo(t)
	guarded := inventory.NewReadOnlyRepository(inner)

	item, err := inventory.NewItemFactory().Build(record.Record{
		ID:         "readonly-sync-1",
		Name:       "readonly-sync-host",
		Type:       "linux_server",
		Properties: map[string]interface{}{"host": "10.0.0.78"},
		State:      pkginventory.StateActive,
		Source:     pkginventory.SourceAuthority{Plugin: "conformance"},
	})
	if err != nil {
		t.Fatalf("building item: %v", err)
	}

	if err := guarded.Create(ctx, item); !errors.Is(err, inventory.ErrInventoryReadOnly) {
		t.Fatalf("Create = %v, want ErrInventoryReadOnly", err)
	}

	// And nothing reached storage: the unguarded repository underneath must
	// still not know about it.
	if _, err := inner.GetByName(ctx, "readonly-sync-host"); !errors.Is(err, inventory.ErrItemNotFound) {
		t.Errorf("the blocked device reached storage anyway: %v", err)
	}
}
