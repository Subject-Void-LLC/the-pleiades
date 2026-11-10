// This file is how the ent-backed SetStore changes a stored Set: Update and
// UpdateWith, and the membership checks Create shares with them.
//
// An update is a fold run under the row's write lock (lockSet), never a write
// of a copy read before it. The PATCH route used to read the Set, apply the
// body and hand the whole Set to Update, so of two PATCHes at once the second
// wrote back the membership it had read before the first committed: one
// removing a device from a shared inventory, which is revoking that device
// from every team the inventory is shared with, could be undone by another
// PATCH that only renamed it. Folding inside the lock makes the second fold
// over what the first wrote. A fold that replaces the members whole from an
// earlier read (Update, which the UI's edit form calls) still overwrites a
// change made since that read: nothing checks a version.
//
// The lock is a write to updated_at, as credstore's lockType is: on SQLite it
// takes the database's write lock, which every other writer waits for; on
// PostgreSQL it takes the row's lock, and the read that follows sees the
// latest committed row because read committed reads each statement afresh.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entgroup "github.com/Subject-Void-LLC/the-pleiades/internal/ent/group"
	entinventory "github.com/Subject-Void-LLC/the-pleiades/internal/ent/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage"
)

// Update saves a Set's editable fields and replaces its membership: an
// UpdateWith whose fold ignores the stored Set.
//
// Membership is replaced rather than merged, because the caller submits the
// whole list: a merge would make removing the last device impossible, since
// an empty submission would be indistinguishable from "no change".
func (s *entSetStore) Update(ctx context.Context, set Set) error {
	_, err := s.UpdateWith(ctx, set.ID, func(Set) (Set, error) { return set, nil })
	return err
}

// UpdateWith folds a change over the Set read under the row's write lock,
// saves the result and returns the Set as read after the commit. See
// SetStore.UpdateWith.
func (s *entSetStore) UpdateWith(ctx context.Context, id int, fold func(stored Set) (Set, error)) (Set, error) {
	if err := storage.NewEntUnitOfWork(s.client).WithTx(ctx, func(txCtx context.Context) error {
		db := ent.FromContext(txCtx)
		stored, err := lockSet(txCtx, db, id)
		if err != nil {
			return err
		}
		// fold gets its own copies of the member lists, so a fold that edits
		// them in place cannot reach the stored Set.
		handed := stored
		handed.GroupIDs = append([]int(nil), stored.GroupIDs...)
		handed.DeviceIDs = append([]int(nil), stored.DeviceIDs...)
		next, err := fold(handed)
		if err != nil {
			return err
		}

		// The organization is read from storage rather than trusted from the
		// fold, so membership is validated against the tenant that actually
		// owns this inventory, and it, the id and the owner are never moved.
		next.ID, next.OrganizationID, next.Owner = stored.ID, stored.OrganizationID, stored.Owner
		if err := checkSetText(next, &stored); err != nil {
			return err
		}
		next.GroupIDs, next.DeviceIDs = distinctIDs(next.GroupIDs), distinctIDs(next.DeviceIDs)
		// Only the members this update adds are checked. A member the
		// inventory already holds is not checked again, so one that has since
		// drifted (an unowned device put in another organization, a group that
		// gained another organization's device) does not block a rename. It
		// is reached by nothing: Set.Selector keeps it out of every job and
		// SetsForDevice out of every grant. That holds too for a member stored
		// before the tenancy check existed, or one that moved between Create's
		// check and its insert, which runs outside a transaction.
		if err := checkMembers(txCtx, db, next.OrganizationID,
			addedIDs(next.GroupIDs, stored.GroupIDs), addedIDs(next.DeviceIDs, stored.DeviceIDs)); err != nil {
			return err
		}

		err = db.Inventory.UpdateOneID(id).
			SetName(next.Name).
			SetDescription(next.Description).
			ClearGroups().
			ClearDevices().
			AddGroupIDs(next.GroupIDs...).
			AddDeviceIDs(next.DeviceIDs...).
			Exec(txCtx)
		switch {
		case ent.IsNotFound(err):
			return fmt.Errorf("%w: %d", ErrSetNotFound, id)
		case ent.IsConstraintError(err):
			return fmt.Errorf("%w: %q", ErrSetExists, next.Name)
		case err != nil:
			return fmt.Errorf("inventory: updating set %d: %w", id, err)
		}
		return nil
	}); err != nil {
		return Set{}, err
	}
	return s.Get(ctx, id)
}

// lockSet takes the write lock on one inventory's row and returns the Set as
// it stands once the lock is held. It must run inside a transaction, whose
// end releases the lock, and asks ent.TxFromContext whether one is open: on
// the root client the lock would end with its own statement.
func lockSet(ctx context.Context, db *ent.Client, id int) (Set, error) {
	if ent.TxFromContext(ctx) == nil {
		return Set{}, errors.New("inventory: a set can only be locked inside a transaction")
	}
	if err := db.Inventory.UpdateOneID(id).SetUpdatedAt(time.Now()).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return Set{}, fmt.Errorf("%w: %d", ErrSetNotFound, id)
		}
		return Set{}, fmt.Errorf("inventory: locking set %d: %w", id, err)
	}
	row, err := db.Inventory.Query().
		Where(entinventory.IDEQ(id)).
		WithOrganization().
		WithGroups().
		WithDevices().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Set{}, fmt.Errorf("%w: %d", ErrSetNotFound, id)
		}
		return Set{}, fmt.Errorf("inventory: loading set %d: %w", id, err)
	}
	return hydrateSet(row), nil
}

// checkMembers refuses a membership naming a group or device that does not
// exist (ErrUnknownMember) or that crosses a tenancy boundary
// (ErrCrossTenantMember, assertMembersInOrganization).
//
// Every check runs before any refusal is returned, and the error wraps each
// sentinel that applies (errors.Join), so how long the answer took does not
// show which check failed first, and the API, which answers both with one
// sentence, cannot be told them apart (owner decision 2026-11-10); the
// queries' cost still differs a little with what they match. The returned
// error still names each kind, for a caller that wants to know. Existence is
// counted because a missing id reaches the join
// table's foreign key as a constraint error, which the write reports as
// ErrSetExists. The errors carry how many, never which. ids must already be
// distinct (distinctIDs), or a repeated id would count as missing.
func checkMembers(ctx context.Context, db *ent.Client, orgID int, groupIDs, deviceIDs []int) error {
	var refusals []error
	if len(groupIDs) > 0 {
		found, err := db.Group.Query().Where(entgroup.IDIn(groupIDs...)).Count(ctx)
		if err != nil {
			return fmt.Errorf("inventory: checking groups: %w", err)
		}
		if missing := len(groupIDs) - found; missing > 0 {
			refusals = append(refusals, fmt.Errorf("%w: %d group(s)", ErrUnknownMember, missing))
		}
	}
	if len(deviceIDs) > 0 {
		found, err := db.Device.Query().Where(entdevice.IDIn(deviceIDs...)).Count(ctx)
		if err != nil {
			return fmt.Errorf("inventory: checking devices: %w", err)
		}
		if missing := len(deviceIDs) - found; missing > 0 {
			refusals = append(refusals, fmt.Errorf("%w: %d device(s)", ErrUnknownMember, missing))
		}
	}
	foreign, err := assertMembersInOrganization(ctx, db, orgID, groupIDs, deviceIDs)
	if err != nil {
		return err
	}
	return errors.Join(append(refusals, foreign...)...)
}

// addedIDs returns the ids in next that stored does not hold, in next's
// order: the members an update adds.
func addedIDs(next, stored []int) []int {
	held := make(map[int]bool, len(stored))
	for _, id := range stored {
		held[id] = true
	}
	var out []int
	for _, id := range next {
		if !held[id] {
			out = append(out, id)
		}
	}
	return out
}

// checkSetText holds set's name and description to the text rules: a name
// that is not blank, and no NUL in either. With stored non-nil it checks only
// a field that differs from stored, so a value an older build stored is never
// refused by an update that leaves it as it is (LESSONS 415, 422).
func checkSetText(set Set, stored *Set) error {
	nameChanged := stored == nil || set.Name != stored.Name
	if nameChanged && strings.TrimSpace(set.Name) == "" {
		return ErrSetNameRequired
	}
	if nameChanged && strings.ContainsRune(set.Name, 0) {
		return &NULError{Field: "name"}
	}
	if (stored == nil || set.Description != stored.Description) && strings.ContainsRune(set.Description, 0) {
		return &NULError{Field: "description"}
	}
	return nil
}

// distinctIDs returns ids with each value once, in first-seen order, and nil
// for an empty list. A membership is a set, and a repeated id used to reach
// the join table twice, whose primary key refused it as a constraint error
// that read as a name already taken.
func distinctIDs(ids []int) []int {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int]bool, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
