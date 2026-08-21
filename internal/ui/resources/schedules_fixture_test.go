package resources_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"

	_ "github.com/mattn/go-sqlite3"
)

// newTestScheduleStore builds a real schedule store over the same shared
// in-memory database the other fixtures use, seeded with one schedule.
//
// A real store rather than a fake, for the reason the template fixture
// gives: the store is where next_run is computed and where a bad recurrence
// is refused, so a fake returning canned rows would let the view's own
// projection drift from what a save actually produces. It also gives the
// list view a row to render, which an empty fake would not.
func newTestScheduleStore(t *testing.T) schedule.Store {
	t.Helper()

	// No t.Cleanup, matching the other fixtures: the view registry is
	// process-wide and the ports it captures outlive whichever test
	// triggered the registration.
	client := enttest.Open(t, "sqlite3", "file:uiaccessfixture?mode=memory&cache=shared&_fk=1")
	ctx := context.Background()

	store := schedule.NewEntStore(client)

	tmpl, err := client.Template.Query().First(ctx)
	if err != nil {
		// The template fixture runs first and seeds two. If it ever stops,
		// this should say so rather than silently register a view with
		// nothing to show.
		t.Fatalf("reading a seeded template to attach a schedule to: %v", err)
	}

	if _, err := store.Create(ctx, schedule.Schedule{
		Name:       "conformance-nightly",
		TemplateID: tmpl.ID,
		Enabled:    true,
		RRule:      "FREQ=DAILY;BYHOUR=2;BYMINUTE=0",
		Exclusions: []string{"FREQ=WEEKLY;BYDAY=SA,SU"},
		Timezone:   "America/New_York",
		DTStart:    time.Date(2024, 3, 8, 7, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seeding a schedule: %v", err)
	}
	return store
}
