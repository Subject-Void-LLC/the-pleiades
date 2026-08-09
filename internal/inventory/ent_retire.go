package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Retire transitions a stored device to inventory.StateArchived and records
// the transition as a Revision, in one transaction.
//
// It reads, decides, and writes inside the transaction rather than around
// it, and the write is conditional on the version the read observed. That
// is the same compare-and-swap shape Save already uses (ent_save.go): a
// concurrent writer that moved the row between this method's read and its
// write matches zero rows and gets ErrVersionConflict rather than silently
// overwriting whatever the other writer did.
func (r *entRepository) Retire(ctx context.Context, name string) error {
	return r.uow.WithTx(ctx, func(txCtx context.Context) error {
		client := r.entClient(txCtx)

		row, err := client.Device.Query().Where(device.NameEQ(name)).Only(txCtx)
		if err != nil {
			if ent.IsNotFound(err) {
				// Wrapped, never returned bare: a caller must be able to
				// tell "this device is already gone" from "the backend
				// failed", which is the same distinction GetByName's own
				// sentinel exists for.
				return fmt.Errorf("retiring %s: %w", name, ErrItemNotFound)
			}
			return fmt.Errorf("failed to load device %s for retirement: %w", name, err)
		}

		// Parse rather than compare strings: an unrecognized state in the
		// column is a real condition (a newer writer, a hand-edited row)
		// and ParseLifecycleState reports it instead of quietly treating
		// it as something it is not.
		current, err := inventory.ParseLifecycleState(row.State)
		if err != nil {
			return fmt.Errorf("device %s holds an unrecognized lifecycle state: %w", name, err)
		}

		// Idempotent by design, matching the HTTP verb this backs. A second
		// retirement is not an error and must not record a second Revision:
		// an audit trail that says a device was archived twice describes
		// something that never happened.
		if current == inventory.StateArchived {
			return nil
		}

		nextVersion := row.Version + 1

		affected, err := client.Device.Update().
			Where(device.DeviceIDEQ(row.DeviceID), device.VersionEQ(row.Version)).
			SetState(inventory.StateArchived.String()).
			SetVersion(nextVersion).
			Save(txCtx)
		if err != nil {
			return fmt.Errorf("failed to retire device %s: %w", name, err)
		}
		if affected == 0 {
			return fmt.Errorf("retiring %s at version %d: %w", name, row.Version, ErrVersionConflict)
		}

		// The lifecycle change is itself an auditable event. Recording it
		// under the "state" field name, with the old and new state as the
		// values, is what lets "when was this device retired, and from
		// what" be answered from the same table every other change is in,
		// rather than from a log line.
		if _, err := client.Revision.Create().
			SetVersion(nextVersion).
			SetChangedAt(time.Now().UTC()).
			SetFieldName(retiredRevisionField).
			SetOldValue(propertyValuePtr(current.String())).
			SetNewValue(propertyValuePtr(inventory.StateArchived.String())).
			SetDeviceID(row.ID).
			Save(txCtx); err != nil {
			return fmt.Errorf("failed to record retirement revision for %s: %w", name, err)
		}

		return nil
	})
}

// retiredRevisionField is the Revision.Field value a lifecycle transition
// records under. It is a constant rather than a literal at each call site
// because both adapters write it and the conformance suite reads it back;
// three independent spellings of one field name is how an audit trail
// becomes unqueryable across backends.
const retiredRevisionField = "state"

// propertyValuePtr adapts a value to the pointer-to-any shape ent's
// optional JSON columns take. ent models an optional JSON column as *any,
// so the pointer carries "is there a value at all" separately from what
// the value is; see ent_save.go's own handling of the same columns.
func propertyValuePtr(v inventory.PropertyValue) *inventory.PropertyValue {
	return &v
}
