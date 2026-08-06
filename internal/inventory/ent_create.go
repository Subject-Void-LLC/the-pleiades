package inventory

import (
	"context"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/device"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// Create inserts a new device row. It is the ent-backed half of the
// Repository write path a sync plugin needs to onboard a device it has
// never seen before.
//
// The type column is Immutable in the schema, so the classification a
// device is created with is the classification it keeps. That is deliberate
// (see the schema's own comment) and it means Create is the only chance to
// get the type right, which is why an item carrying no resolvable type is
// rejected here rather than stored as a row nothing can hydrate later.
func (r *entRepository) Create(ctx context.Context, item inventory.InventoryItem) error {
	deviceType, err := itemDeviceType(item)
	if err != nil {
		return err
	}

	return r.uow.WithTx(ctx, func(txCtx context.Context) error {
		client := r.entClient(txCtx)

		// Check both unique columns before inserting so a duplicate comes
		// back as ErrItemExists rather than as a driver-specific
		// constraint-violation string the caller would have to pattern
		// match on. The insert below is still the real guard against a
		// concurrent writer winning the race between this query and it;
		// this check exists to make the common case diagnosable, not to
		// replace the constraint.
		exists, err := client.Device.Query().
			Where(device.Or(
				device.DeviceIDEQ(string(item.ID())),
				device.NameEQ(item.Name()),
			)).Exist(txCtx)
		if err != nil {
			return fmt.Errorf("failed to check whether device %s already exists: %w", item.Name(), err)
		}
		if exists {
			return fmt.Errorf("creating %s: %w", item.Name(), ErrItemExists)
		}

		create := client.Device.Create().
			SetDeviceID(string(item.ID())).
			SetName(item.Name()).
			SetType(deviceType).
			SetProperties(item.Properties().Raw()).
			SetVersion(item.Version()).
			SetState(item.State().String()).
			SetTags(tagsToStrings(item.Tags()))

		// Source is optional in the schema, and writing an empty plugin
		// name would turn "provenance unknown" into a stored empty string
		// that later reads cannot tell apart from a real value.
		if src := item.Source(); src.Plugin != "" {
			create = create.SetSource(src.Plugin)
			if !src.SyncedAt.IsZero() {
				create = create.SetSourceSyncedAt(src.SyncedAt)
			}
		}

		row, err := create.Save(txCtx)
		if err != nil {
			if ent.IsConstraintError(err) {
				return fmt.Errorf("creating %s: %w", item.Name(), ErrItemExists)
			}
			return fmt.Errorf("failed to create device %s: %w", item.Name(), err)
		}

		// A created item may already carry history when it was built from a
		// Record that had some (a plugin replaying a device it previously
		// exported, for instance). Persist it rather than dropping it: the
		// audit trail is the one thing that cannot be reconstructed later.
		return createRevisions(txCtx, client, row, item.History())
	})
}

// createRevisions writes every revision in revs against an already-inserted
// device row. It is split out of Create so the transaction body stays
// readable and so the loop's FK handling lives next to nothing else.
func createRevisions(ctx context.Context, client *ent.Client, row *ent.Device, revs []inventory.Revision) error {
	for _, rev := range revs {
		create := client.Revision.Create().
			SetVersion(rev.Version).
			SetChangedAt(rev.ChangedAt).
			SetFieldName(rev.Field).
			SetDeviceID(row.ID)

		// A removal records a nil NewValue, and a first write records a nil
		// OldValue. Setting a typed nil through ent's pointer setters would
		// store a JSON null rather than leaving the column absent, so each
		// side is set only when it actually carries a value.
		if rev.OldValue != nil {
			old := rev.OldValue
			create = create.SetOldValue(&old)
		}
		if rev.NewValue != nil {
			nv := rev.NewValue
			create = create.SetNewValue(&nv)
		}

		if _, err := create.Save(ctx); err != nil {
			return fmt.Errorf("failed to write revision %d for device %s: %w", rev.Version, row.Name, err)
		}
	}
	return nil
}
