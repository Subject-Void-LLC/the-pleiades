// This file is how the ent-backed Store changes a stored announcement: Update
// and UpdateWith, and the text rule Create shares with them.
//
// An update is a fold run under the row's write lock (lockAnnouncement),
// never a write of a copy read before it, so of two PATCHes at once the
// second folds over what the first wrote: one extending a change freeze's
// end and one correcting its title both land. The lock is a write to
// updated_at, as the inventory store's and credstore's are: on SQLite it
// takes the database's write lock, and on PostgreSQL the row's, after which
// read committed reads the latest committed row.
package announce

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entannouncement "github.com/Subject-Void-LLC/the-pleiades/internal/ent/announcement"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage"
)

// Update saves an announcement's editable fields: an UpdateWith whose fold
// ignores the stored announcement.
//
// The author is not among them: it is immutable by schema, because an
// attribution somebody can rewrite is an attribution nobody can rely on.
func (s *entStore) Update(ctx context.Context, a Announcement) error {
	_, err := s.UpdateWith(ctx, a.ID, func(Announcement) (Announcement, error) { return a, nil })
	return err
}

// UpdateWith folds a change over the announcement read under the row's write
// lock, saves the result and returns the announcement as read after the
// commit. See Store.UpdateWith.
func (s *entStore) UpdateWith(ctx context.Context, id int, fold func(stored Announcement) (Announcement, error)) (Announcement, error) {
	if err := storage.NewEntUnitOfWork(s.client).WithTx(ctx, func(txCtx context.Context) error {
		db := ent.FromContext(txCtx)
		stored, err := lockAnnouncement(txCtx, db, id)
		if err != nil {
			return err
		}
		next, err := fold(stored)
		if err != nil {
			return err
		}
		// Kept as stored whatever the fold returned: rewriting an
		// attribution is what makes one worthless, and moving an
		// announcement between tenants would silently change who it reaches.
		next.ID, next.Author, next.OrganizationID = stored.ID, stored.Author, stored.OrganizationID
		if err := checkText(next, &stored); err != nil {
			return err
		}

		update := db.Announcement.UpdateOneID(id).
			SetTitle(next.Title).
			SetBody(next.Body).
			SetLevel(string(ParseLevel(string(next.Level))))
		// Clear-then-set, so removing an end date is expressible. Without the
		// clear, a nil pointer would be indistinguishable from "leave it
		// alone" and an announcement could never be made permanent again.
		if next.StartsAt != nil {
			update = update.SetStartsAt(*next.StartsAt)
		} else {
			update = update.ClearStartsAt()
		}
		if next.EndsAt != nil {
			update = update.SetEndsAt(*next.EndsAt)
		} else {
			update = update.ClearEndsAt()
		}
		if err := update.Exec(txCtx); err != nil {
			return fmt.Errorf("announce: updating announcement %d: %w", id, err)
		}
		return nil
	}); err != nil {
		return Announcement{}, err
	}
	return s.Get(ctx, id)
}

// lockAnnouncement takes the write lock on one announcement's row and
// returns it as it stands once the lock is held. It must run inside a
// transaction, whose end releases the lock: on the root client the lock
// would end with its own statement.
func lockAnnouncement(ctx context.Context, db *ent.Client, id int) (Announcement, error) {
	if ent.TxFromContext(ctx) == nil {
		return Announcement{}, errors.New("announce: an announcement can only be locked inside a transaction")
	}
	if err := db.Announcement.UpdateOneID(id).SetUpdatedAt(time.Now()).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return Announcement{}, fmt.Errorf("%w: %d", ErrNotFound, id)
		}
		return Announcement{}, fmt.Errorf("announce: locking announcement %d: %w", id, err)
	}
	row, err := db.Announcement.Query().Where(entannouncement.IDEQ(id)).WithOrganization().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Announcement{}, fmt.Errorf("%w: %d", ErrNotFound, id)
		}
		return Announcement{}, fmt.Errorf("announce: loading announcement %d: %w", id, err)
	}
	return hydrate(row), nil
}

// checkText holds a's title and body to the text rules: not blank, and no
// NUL. With stored non-nil it checks only a field that differs from stored,
// so a value an older build stored is never refused by an update that
// leaves it as it is (LESSONS 415, 422).
func checkText(a Announcement, stored *Announcement) error {
	titleChanged := stored == nil || a.Title != stored.Title
	bodyChanged := stored == nil || a.Body != stored.Body
	switch {
	case titleChanged && strings.TrimSpace(a.Title) == "":
		return ErrTitleRequired
	case titleChanged && strings.ContainsRune(a.Title, 0):
		return &NULError{Field: "title"}
	case bodyChanged && strings.TrimSpace(a.Body) == "":
		return ErrBodyRequired
	case bodyChanged && strings.ContainsRune(a.Body, 0):
		return &NULError{Field: "body"}
	}
	return nil
}
