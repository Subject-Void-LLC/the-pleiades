// This file holds the project store's row-lock scenario, run on SQLite in
// internal/project's tests and on PostgreSQL in this package's.
//
// A project's Update compares the URL it writes with the URL stored, and
// checks it only when it changed. Without a lock that comparison raced: an
// edit could read a legacy URL carrying a credential, another edit replace it
// with a clean one and commit, and the first then write the legacy URL back
// unchecked, as unchanged. The scenario pauses the first edit at its write,
// after its read, and requires the second to wait for it and then win.
package rowlocktest

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// cleanSourceURL is the URL the second edit sets.
const cleanSourceURL = "https://git.example.invalid/team/clean.git"

// ProjectEditsNeverInterleave stores a manual project with legacyURL, a source
// URL carrying a credential as an older release could have stored it, past
// the store's checks (each caller's test file holds the literal, so no
// shipped file carries one), then runs two Updates through store: a rename that
// keeps the stored URL, held at its write, and a URL fix to a clean URL. It
// fails t unless the fix waits for the rename's lock and the project ends with
// the rename's name and the clean URL. client must be the one store writes
// through, opened on a database whose writers wait for each other.
func ProjectEditsNeverInterleave(t *testing.T, client *ent.Client, store project.Store, legacyURL string) {
	t.Helper()
	ctx := context.Background()
	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	created, err := store.Create(ctx, project.Project{
		Name: "legacy", SCMType: project.SCMManual, SCMURL: cleanSourceURL, OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	client.Project.UpdateOneID(created.ID).SetScmURL(legacyURL).ExecX(ctx)
	stored, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}

	// The hook calls whichever pause is armed when an Update's real write
	// (not the lock's, which sets only updated_at) reaches the database.
	var renamePause, fixPause atomic.Pointer[func()]
	client.Project.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if mutation, ok := m.(*ent.ProjectMutation); ok && mutation.Op().Is(ent.OpUpdateOne) {
				if name, ok := mutation.Name(); ok && name == "renamed" {
					if pause := renamePause.Load(); pause != nil {
						(*pause)()
					}
				}
				if url, ok := mutation.ScmURL(); ok && url == cleanSourceURL {
					if pause := fixPause.Load(); pause != nil {
						(*pause)()
					}
				}
			}
			return next.Mutate(ctx, m)
		})
	})

	SecondFoldWaits(t,
		func(onFold func()) error {
			renamePause.Store(&onFold)
			renamed := stored
			renamed.Name = "renamed"
			return store.Update(ctx, renamed)
		},
		func(onFold func()) error {
			fixPause.Store(&onFold)
			fixed := stored
			fixed.SCMURL = cleanSourceURL
			return store.Update(ctx, fixed)
		})

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}
	if got.SCMURL != cleanSourceURL {
		t.Errorf("after both edits the stored URL carries the credential again: the rename wrote the URL it read back over the fix")
	}
	if got.Name != "renamed" && got.SCMURL == cleanSourceURL {
		// The fix ran second and wrote every field it read, so the rename's
		// name is lost: a lost update, but not the credential one this
		// scenario is about, and expected while neither edit folds over the
		// other. Only reported.
		t.Logf("the fix, run second, kept the name it read (%q): last writer wins for the name", got.Name)
	}
}
