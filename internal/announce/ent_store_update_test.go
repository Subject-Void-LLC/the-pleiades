// This file holds the ent-backed Store's UpdateWith to its contract: a fold
// runs on the announcement as it stands under the row's lock, two updates of
// one announcement never interleave, the author and organization are kept,
// and the result needs a title and a body.
//
// The concurrency test runs internal/storage/rowlocktest's scenario, which
// that package also runs on PostgreSQL, on a real SQLite file opened through
// ent.OpenEmbedded, the Controller's own path: a shared-cache in-memory database answers a
// contending writer with SQLITE_LOCKED, which no busy timeout retries.
package announce_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage/rowlocktest"
)

// openStoreFile opens a Store over a fresh SQLite file.
func openStoreFile(t *testing.T) (announce.Store, *ent.Client) {
	t.Helper()
	client, err := ent.OpenEmbedded(context.Background(), filepath.Join(t.TempDir(), "announce.db"))
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return announce.NewEntStore(client), client
}

// TestEntStore_UpdateWithFoldsOverTheOtherUpdatesWrite holds one update
// inside its fold while a second starts. The second must not fold until the
// first has committed, and must fold over the title the first wrote, so
// both changes land.
func TestEntStore_UpdateWithFoldsOverTheOtherUpdatesWrite(t *testing.T) {
	t.Parallel()
	store, _ := openStoreFile(t)
	ctx := context.Background()
	created := mustCreate(t, store, announce.Announcement{Title: "Freeze", Body: "No dispatches.", Author: "alice"})

	var sawTitle string
	rowlocktest.SecondFoldWaits(t,
		func(onFold func()) error {
			_, err := store.UpdateWith(ctx, created.ID, func(a announce.Announcement) (announce.Announcement, error) {
				onFold()
				a.Title = "Freeze, extended"
				return a, nil
			})
			return err
		},
		func(onFold func()) error {
			_, err := store.UpdateWith(ctx, created.ID, func(a announce.Announcement) (announce.Announcement, error) {
				onFold()
				sawTitle = a.Title
				a.Level = announce.LevelCritical
				return a, nil
			})
			return err
		})

	if sawTitle != "Freeze, extended" {
		t.Errorf("the second fold saw title %q, want the first update's", sawTitle)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Title != "Freeze, extended" || got.Level != announce.LevelCritical {
		t.Errorf("after both updates: %+v, want the first's title and the second's level", got)
	}
}

// TestEntStore_UpdateWithKeepsWhatItMustAndRefusesBlankText covers the rest
// of the contract: a blank title or body and a fold's own error are refused,
// leaving the announcement as it was; a moved author, organization or id is
// kept as stored; and a missing announcement is not found, with no fold.
func TestEntStore_UpdateWithKeepsWhatItMustAndRefusesBlankText(t *testing.T) {
	t.Parallel()
	store, client := openStoreFile(t)
	ctx := context.Background()
	org, other := newOrg(t, client, "acme"), newOrg(t, client, "globex")
	created := mustCreate(t, store, announce.Announcement{Title: "Freeze", Body: "No dispatches.", Author: "alice", OrganizationID: org})
	refused := errors.New("refused by the fold")

	cases := map[string]struct {
		change func(*announce.Announcement)
		err    error
		want   error
	}{
		"a blank title":        {change: func(a *announce.Announcement) { a.Title = " " }, want: announce.ErrTitleRequired},
		"a blank body":         {change: func(a *announce.Announcement) { a.Body = "\n" }, want: announce.ErrBodyRequired},
		"the fold's own error": {err: refused, want: refused},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := store.UpdateWith(ctx, created.ID, func(a announce.Announcement) (announce.Announcement, error) {
				if tc.err != nil {
					return announce.Announcement{}, tc.err
				}
				tc.change(&a)
				return a, nil
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("UpdateWith() error = %v, want %v", err, tc.want)
			}
			if got, _ := store.Get(ctx, created.ID); got.Title != "Freeze" || got.Body != "No dispatches." {
				t.Errorf("a refused update changed the announcement: %+v", got)
			}
		})
	}

	got, err := store.UpdateWith(ctx, created.ID, func(a announce.Announcement) (announce.Announcement, error) {
		a.ID, a.Author, a.OrganizationID = 4242, "mallory", other
		return a, nil
	})
	if err != nil {
		t.Fatalf("UpdateWith() error = %v", err)
	}
	if got.ID != created.ID || got.Author != "alice" || got.OrganizationID != org {
		t.Errorf("UpdateWith() = %+v, want the id, author and organization kept", got)
	}

	called := false
	if _, err := store.UpdateWith(ctx, 4242, func(a announce.Announcement) (announce.Announcement, error) {
		called = true
		return a, nil
	}); !errors.Is(err, announce.ErrNotFound) || called {
		t.Errorf("UpdateWith() of a missing announcement = %v, fold called %v; want ErrNotFound and no call", err, called)
	}
}

// TestEntStore_CreateRefusesAnUnknownOrganization is a typed refusal, where
// the foreign key failure used to be the caller's only answer.
func TestEntStore_CreateRefusesAnUnknownOrganization(t *testing.T) {
	t.Parallel()
	store, client := openStoreFile(t)
	_, err := store.Create(context.Background(), announce.Announcement{Title: "t", Body: "b", Author: "alice", OrganizationID: 999999})
	if !errors.Is(err, announce.ErrUnknownOrganization) {
		t.Errorf("Create() error = %v, want ErrUnknownOrganization", err)
	}
	if n := client.Announcement.Query().CountX(context.Background()); n != 0 {
		t.Errorf("a refused create stored %d announcements", n)
	}
}

// TestEntStore_StoreFailuresAreReportedNotMisread covers the failures the
// store cannot classify: a write the database refuses inside UpdateWith,
// and a closed database under Create's organization check. Each is returned
// as an error, never read as not found or as a refusal of the caller's text.
func TestEntStore_StoreFailuresAreReportedNotMisread(t *testing.T) {
	t.Parallel()
	store, client := openStoreFile(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")
	created := mustCreate(t, store, announce.Announcement{Title: "Freeze", Body: "No dispatches.", Author: "alice"})

	// A hook refusing any update that sets a title stands in for a database
	// refusing the write; the lock's own update sets only updated_at.
	refused := errors.New("the database refused the write")
	client.Announcement.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if mutation, ok := m.(*ent.AnnouncementMutation); ok && mutation.Op().Is(ent.OpUpdateOne) {
				if _, setsTitle := mutation.Title(); setsTitle {
					return nil, refused
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err := store.UpdateWith(ctx, created.ID, func(a announce.Announcement) (announce.Announcement, error) {
		a.Title = "Freeze, extended"
		return a, nil
	})
	if !errors.Is(err, refused) || errors.Is(err, announce.ErrNotFound) {
		t.Errorf("UpdateWith() error = %v, want the database's refusal", err)
	}
	if got, _ := store.Get(ctx, created.ID); got.Title != "Freeze" {
		t.Errorf("a refused write changed the title to %q", got.Title)
	}

	_ = client.Close()
	_, err = store.Create(ctx, announce.Announcement{Title: "t", Body: "b", Author: "alice", OrganizationID: org})
	if err == nil || errors.Is(err, announce.ErrUnknownOrganization) {
		t.Errorf("Create() on a closed database error = %v, want a failure that is not ErrUnknownOrganization", err)
	}
}

// TestEntStore_UpdateWithLeavesAStoredBlankTitleAlone is LESSONS 415 and
// 422's rule for announcements: a title an older build stored blank does not
// block an update that only ends the window, and a NUL or blank value the
// update writes is still refused.
func TestEntStore_UpdateWithLeavesAStoredBlankTitleAlone(t *testing.T) {
	t.Parallel()
	store, client := openStoreFile(t)
	ctx := context.Background()
	// Written past the store, as the old PATCH route could.
	id := client.Announcement.Create().SetTitle("  ").SetBody("No dispatches.").SetAuthor("alice").SaveX(ctx).ID

	ends := time.Date(2031, 1, 12, 0, 0, 0, 0, time.UTC)
	if _, err := store.UpdateWith(ctx, id, func(a announce.Announcement) (announce.Announcement, error) {
		a.EndsAt = &ends
		return a, nil
	}); err != nil {
		t.Fatalf("UpdateWith() ending the window = %v, want it applied", err)
	}
	var nul *announce.NULError
	if _, err := store.UpdateWith(ctx, id, func(a announce.Announcement) (announce.Announcement, error) {
		a.Body = "b\x00"
		return a, nil
	}); !errors.As(err, &nul) || nul.Field != "body" {
		t.Errorf("UpdateWith() writing a NUL = %v, want a NULError for body", err)
	}
}
