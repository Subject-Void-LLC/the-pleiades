package resources_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"

	// The built-in launchable types, blank-imported as cmd/controller does:
	// their init() functions are the only thing that registers them, and the
	// schedule store refuses a target whose type is not registered.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types"
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
func newTestScheduleStore(t *testing.T) (schedule.Store, launchable.Store) {
	t.Helper()

	// No t.Cleanup, matching the other fixtures: the view registry is
	// process-wide and the ports it captures outlive whichever test
	// triggered the registration.
	client := enttest.Open(t, "sqlite3", "file:uiaccessfixture?mode=memory&cache=shared&_fk=1")
	ctx := context.Background()

	// The launchable store, and the admission the real controller composes
	// around it. The Router is nil here on purpose: a fixture has no
	// launchers, and a nil Router means "nothing to object to in advance",
	// which is the honest reading rather than a stubbed approval.
	launchables := launchable.NewEntStore(client)
	store := schedule.NewEntStore(client, launchable.Admission{Store: launchables})

	tmpl, err := client.Template.Query().WithLaunchable().First(ctx)
	if err != nil {
		// The template fixture runs first and seeds two. If it ever stops,
		// this should say so rather than silently register a view with
		// nothing to show.
		t.Fatalf("reading a seeded template to attach a schedule to: %v", err)
	}

	if tmpl.Edges.Launchable == nil {
		t.Fatal("the seeded template has no launchable row, so nothing could schedule it")
	}

	// A real project too, through its real store, so the RUNS picker has one of
	// each sort to offer and the grouping has something to group. The Projects
	// view drives a fake store of its own; this row exists for the launchable
	// listing, which reads the database.
	if _, err := project.NewEntStore(client, project.SourcePolicy{}).Create(ctx, project.Project{
		Name:           "conformance-automation",
		SCMType:        project.SCMGit,
		SCMURL:         "https://git.example.test/team/automation.git",
		OrganizationID: tmpl.Edges.Launchable.QueryOrganization().OnlyIDX(ctx),
	}); err != nil {
		t.Fatalf("seeding a project to schedule: %v", err)
	}

	// Everything() as the reach: the fixture is seeding, not exercising the
	// authorization check, and a Reach that admitted nothing would fail every
	// seed. The check itself is covered by its own tests.
	if _, err := store.Create(ctx, schedule.Schedule{
		Name:         "conformance-nightly",
		LaunchableID: tmpl.Edges.Launchable.ID,
		Enabled:      true,
		RRule:        "FREQ=DAILY;BYHOUR=2;BYMINUTE=0",
		Exclusions:   []string{"FREQ=WEEKLY;BYDAY=SA,SU"},
		Timezone:     "America/New_York",
		DTStart:      time.Date(2024, 3, 8, 7, 0, 0, 0, time.UTC),
	}, launchable.Everything()); err != nil {
		t.Fatalf("seeding a schedule: %v", err)
	}
	return store, launchables
}
