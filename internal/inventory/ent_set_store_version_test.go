// This file holds the ent-backed SetStore's version to its contract: a new Set
// is at version 0, each committed UpdateWith moves it by exactly one, and a
// refused or failed update, or the write lock alone, never moves it.
package inventory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// TestEntSetStore_VersionMovesOnlyOnCommit runs every way an update can end
// against the version a caller would hold. A refused fold (ErrVersionStale
// among them) and a write the store refuses each take the row's lock, which
// writes updated_at, and must leave the version where it was: a version the
// lock moved would tell every holder of a fresh copy that it is stale. A
// committed update moves it by one, whatever Version the fold returns.
func TestEntSetStore_VersionMovesOnlyOnCommit(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")
	created, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: org})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Version != 0 {
		t.Fatalf("Create() version = %d, want 0", created.Version)
	}
	if _, err := store.Create(ctx, inventory.Set{Name: "taken", OrganizationID: org}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	refusals := map[string]func(inventory.Set) (inventory.Set, error){
		"a stale version": func(inventory.Set) (inventory.Set, error) {
			return inventory.Set{}, fmt.Errorf("%w", inventory.ErrVersionStale)
		},
		"a blank name": func(s inventory.Set) (inventory.Set, error) {
			s.Name = " "
			return s, nil
		},
		"an unknown member": func(s inventory.Set) (inventory.Set, error) {
			s.DeviceIDs = []int{999999}
			return s, nil
		},
		// Refused by the write itself, the unique index, after the fold and
		// every check have passed: the version moved in that same statement
		// must roll back with it.
		"a taken name": func(s inventory.Set) (inventory.Set, error) {
			s.Name = "taken"
			return s, nil
		},
	}
	for name, fold := range refusals {
		if _, err := store.UpdateWith(ctx, created.ID, fold); err == nil {
			t.Fatalf("%s: UpdateWith() succeeded, want a refusal", name)
		}
		if got, _ := store.Get(ctx, created.ID); got.Version != 0 {
			t.Errorf("%s: version after a refused update = %d, want 0", name, got.Version)
		}
	}

	var sawVersion uint64 = 42
	updated, err := store.UpdateWith(ctx, created.ID, func(s inventory.Set) (inventory.Set, error) {
		sawVersion = s.Version
		s.Description = "routers"
		s.Version = 99
		return s, nil
	})
	if err != nil {
		t.Fatalf("UpdateWith() error = %v", err)
	}
	if sawVersion != 0 {
		t.Errorf("the fold saw version %d, want the stored 0", sawVersion)
	}
	if updated.Version != 1 {
		t.Errorf("version after one committed update = %d, want 1 (the fold's 99 ignored)", updated.Version)
	}

	// A second commit that changes nothing still moves it: the store does
	// not compare, so a caller's copy is never wrongly called current.
	again, err := store.UpdateWith(ctx, created.ID, func(s inventory.Set) (inventory.Set, error) { return s, nil })
	if err != nil {
		t.Fatalf("UpdateWith() error = %v", err)
	}
	if again.Version != 2 {
		t.Errorf("version after a second commit = %d, want 2", again.Version)
	}
}
