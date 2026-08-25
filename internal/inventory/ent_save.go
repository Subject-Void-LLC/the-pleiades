package inventory

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Save persists an item's properties, lifecycle state, and new revisions in
// one transaction, conditional on the stored version still matching the one
// the item was hydrated at.
//
// The whole method exists to close a specific hole: before it, Repository
// was read-only, so a Revision recorded by AddInfo lived in a process's
// memory and was discarded on exit. Versioning, history, and lifecycle were
// correct Go types with nowhere to be stored.
func (r *entRepository) Save(ctx context.Context, item inventory.InventoryItem) error {
	// An item that cannot report the version it was loaded at cannot be
	// written safely, because there is no way to detect a lost update. Fail
	// loudly rather than writing unconditionally and hoping.
	v, ok := item.(versioned)
	if !ok {
		return fmt.Errorf("item %s does not report a base version, refusing to write without conflict detection", item.Name())
	}

	baseVersion := v.BaseVersion()
	current := item.Version()

	// Nothing changed since load. Writing anyway would burn a version and
	// produce a revision-free bump that later readers cannot explain.
	if current == baseVersion {
		return nil
	}
	if current < baseVersion {
		return fmt.Errorf("item %s has version %d below its loaded version %d, which should be impossible", item.Name(), current, baseVersion)
	}

	// Only the revisions recorded since load are new. Anything at or below
	// baseVersion is already in the table and must not be duplicated.
	var pending []inventory.Revision
	for _, rev := range item.History() {
		if rev.Version > baseVersion {
			pending = append(pending, rev)
		}
	}

	return r.uow.WithTx(ctx, func(txCtx context.Context) error {
		client := r.entClient(txCtx)

		// The row's internal primary key, resolved once and used twice:
		// by the conditional update below and by the revision edge further
		// down. That second lookup already existed; hoisting it is what
		// lets this write name exactly one row.
		//
		// Phase 78c is why this is UpdateOneID rather than the bulk
		// Update builder it used to be. Device.properties is now sealed
		// against the row's own secret_binding, and a bulk update cannot
		// read that column: ent exposes OldSecretBinding on UpdateOne
		// alone. That is not a limitation being worked around, it is the
		// same rule the credential store already lives under, and it says
		// something true about this call, which always meant one row and
		// merely expressed itself as a predicate over many.
		devRow, err := client.Device.Query().
			Where(device.DeviceIDEQ(string(item.ID()))).
			Only(txCtx)
		if err != nil {
			if ent.IsNotFound(err) {
				// The row is gone. Same conclusion as a version that
				// moved: the change was computed against state that no
				// longer exists, so the caller must reload.
				return fmt.Errorf("saving %s at version %d: %w", item.Name(), baseVersion, ErrVersionConflict)
			}
			return fmt.Errorf("failed to resolve internal id for %s: %w", item.Name(), err)
		}

		// The conditional update. Matching on the loaded version is what
		// makes this compare-and-swap rather than last-write-wins: if
		// another writer already moved the row, this matches nothing.
		update := client.Device.UpdateOneID(devRow.ID).
			Where(device.VersionEQ(baseVersion)).
			SetProperties(item.Properties().Raw()).
			SetVersion(current).
			SetState(item.State().String()).
			SetTags(tagsToStrings(item.Tags()))

		// Chain audit finding (IMPLEMENTATION.md Phase W4): Save never
		// wrote source/source_synced_at/tags, so a value carried by a
		// hydrated item, correct only by construction, silently failed to
		// round-trip through any future mutation of it. Nothing in the
		// base InventoryItem contract can change Source today, so this
		// writes back exactly what was already loaded; the point is that
		// the column stops being write-blind, not that this call changes
		// its value. Only set when non-empty/non-zero so an item with no
		// source authority does not turn a NULL column into a stored
		// empty string or a year-1 timestamp.
		if src := item.Source(); src.Plugin != "" {
			update = update.SetSource(src.Plugin)
			if !src.SyncedAt.IsZero() {
				update = update.SetSourceSyncedAt(src.SyncedAt)
			}
		}

		if _, err := update.Save(txCtx); err != nil {
			if ent.IsNotFound(err) {
				// The version moved between the lookup above and this
				// write, so the change was computed against state that no
				// longer exists and the caller must reload rather than
				// retry. UpdateOne reports an unmatched predicate as a
				// not-found rather than as zero rows affected, which is
				// the one behavioural difference from the bulk builder
				// this call used before Phase 78c.
				return fmt.Errorf("saving %s at version %d: %w", item.Name(), baseVersion, ErrVersionConflict)
			}
			return fmt.Errorf("failed to update device %s: %w", item.Name(), err)
		}

		if len(pending) == 0 {
			return nil
		}

		for _, rev := range pending {
			create := client.Revision.Create().
				SetVersion(rev.Version).
				SetChangedAt(rev.ChangedAt).
				SetFieldName(rev.Field).
				SetDeviceID(devRow.ID)
			// A removal records a nil NewValue. Setting a typed nil through
			// ent's optional JSON setter would store a JSON null rather than
			// leaving the column absent, so skip the setter entirely and let
			// the column stay NULL, which is what "there is no new value" means.
			// ent models an optional JSON column as *any, so the pointer itself
			// carries "is there a value at all", separately from whatever the
			// value is. Take the address of a local copy rather than of the
			// loop variable's field.
			if rev.OldValue != nil {
				old := rev.OldValue
				create = create.SetOldValue(&old)
			}
			if rev.NewValue != nil {
				nv := rev.NewValue
				create = create.SetNewValue(&nv)
			}
			if _, err := create.Save(txCtx); err != nil {
				return fmt.Errorf("failed to record revision %d for %s: %w", rev.Version, item.Name(), err)
			}
		}
		return nil
	})
}

// loadRevisions fetches a device's stored audit trail, oldest first, so a
// hydrated item carries the history that already exists rather than
// appearing to have never changed.
func loadRevisions(ctx context.Context, dev *ent.Device) ([]inventory.Revision, error) {
	rows, err := dev.QueryRevisions().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load revisions for %s: %w", dev.Name, err)
	}

	revs := make([]inventory.Revision, 0, len(rows))
	for _, row := range rows {
		rev := inventory.Revision{
			Version:   row.Version,
			ChangedAt: row.ChangedAt,
			Field:     row.FieldName,
		}
		// ent models an optional JSON column as a pointer, so a NULL column
		// arrives as a nil pointer rather than a nil value.
		if row.OldValue != nil {
			rev.OldValue = *row.OldValue
		}
		if row.NewValue != nil {
			rev.NewValue = *row.NewValue
		}
		revs = append(revs, rev)
	}

	// Order by version, not by changed_at: version is monotonic per device
	// and wall-clock time can move backward.
	sortRevisionsByVersion(revs)
	return revs, nil
}

// sortRevisionsByVersion orders an audit trail oldest first. It is a plain
// insertion sort because an audit trail arrives nearly sorted already and
// is small per device.
func sortRevisionsByVersion(revs []inventory.Revision) {
	for i := 1; i < len(revs); i++ {
		for j := i; j > 0 && revs[j-1].Version > revs[j].Version; j-- {
			revs[j-1], revs[j] = revs[j], revs[j-1]
		}
	}
}
