// A second device, inventory and schedule for the view conformance fixture,
// so the paging test has a next page to render for each.
package resources_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// This file gives three views a second record: Devices, Inventories and
// Schedules.
//
// One record is the count at which the paging assertion cannot fail. At
// limit=1 a single record fills its page and has nothing after it, so the
// reader never names a next cursor, the control that leads to page two is
// never rendered, and the paging conformance test skips rather than checks
// anything. The access
// fixture seeds two of everything for the same reason; these three views
// read ports of their own, so each needs its own second row.
//
// Each second record is written through the path its first one takes into
// the same store: the fake repository's and the fake set store's own
// Create, and the real schedule store's Create with its validation and
// admission intact. Nothing here writes around a store.
//
// Each is also added AFTER the seeded first record, and sorts after it in
// the order its view lists. Several assertions open a view's first record
// (the drill-down walk, the detail page, the edit-form round trip, the
// viewer's missing delete control), and those should keep reading the
// record they were written against rather than whichever row this file
// happened to add.

// seedSecondRecords adds the second device, inventory and schedule.
//
// It runs inside registerOnce, after the fixtures it builds on, so like
// every other seed it happens once per process and the rows persist for
// every later test, including the second iteration of `go test -count=2`.
func seedSecondRecords(t *testing.T, repo *fakeRepository, sets *fakeSetStore,
	schedules schedule.Store, launchables launchable.Store) {
	t.Helper()
	ctx := context.Background()

	seedSecondDevice(ctx, t, repo)
	seedSecondInventory(ctx, t, sets)
	seedSecondSchedule(ctx, t, schedules, launchables)
}

// seedSecondDevice adds a second device to the fake repository.
//
// It is built by the real item factory from a record shaped like the first
// one, so it is a device type the Devices view genuinely knows how to
// project, and it goes in through the repository's own Create. The ID sorts
// after the first device's, which is the order a real repository pages in.
func seedSecondDevice(ctx context.Context, t *testing.T, repo *fakeRepository) {
	t.Helper()

	item, err := inventory.NewItemFactory().Build(record.Record{
		ID:     "01890000-0000-7000-8000-000000000002",
		Name:   "core-router-02",
		Type:   "linux_server",
		Tags:   []pkginventory.Tag{"core"},
		State:  pkginventory.StateActive,
		Source: pkginventory.SourceAuthority{Plugin: "conformance"},
	})
	if err != nil {
		t.Fatalf("building the second conformance device: %v", err)
	}
	if err := repo.Create(ctx, item); err != nil {
		t.Fatalf("seeding the second conformance device: %v", err)
	}
}

// seedSecondInventory adds a second inventory to the fake set store.
//
// OrganizationName is set for the reason newFakeSetStore gives about the
// first one: the real store always loads it, and a row without it would
// fail the names-not-keys assertion for a reason no real inventory has.
//
// The name is not "production", on purpose. The template launch form's
// inventory picker labels each option "name (organization)", and a survey
// test finds the "production (conformance)" option by that label, so a
// second option with the same label would make its lookup ambiguous.
//
// Its members are ids ListMembers already offers, so its Devices and
// Groups sections resolve every id to a name rather than to nothing.
func seedSecondInventory(ctx context.Context, t *testing.T, sets *fakeSetStore) {
	t.Helper()

	if _, err := sets.Create(ctx, inventory.Set{
		Name:        "conformance-staging",
		Description: "The fleet changes are tried on first.",
		// Organization 1 is the tenant the first inventory belongs to, and
		// the name is the one that first inventory carries for it.
		OrganizationID: 1, OrganizationName: "conformance",
		Owner:    "conformance",
		GroupIDs: []int{8}, DeviceIDs: []int{13},
	}); err != nil {
		t.Fatalf("seeding the second conformance inventory: %v", err)
	}
}

// seedSecondSchedule adds a second schedule, attached to the project the
// schedule fixture seeded rather than to a template.
//
// A project target means the Schedules list holds one schedule of each
// launchable type, which is the case the RUNS column and picker exist for.
// The project is found through the launchables read port, the same one the
// RUNS picker reads, rather than by reaching into the database for its row.
//
// The name matters. The store lists by name, and "conformance-weekly" sorts
// after "conformance-nightly", so the seeded schedule stays the first record
// every open-the-first-record assertion reaches.
func seedSecondSchedule(ctx context.Context, t *testing.T, schedules schedule.Store, launchables launchable.Store) {
	t.Helper()

	projects, err := launchables.List(ctx, launchable.Query{Types: []string{launchable.TypeProject}})
	if err != nil {
		t.Fatalf("listing launchable projects: %v", err)
	}
	var target launchable.Target
	for _, p := range projects {
		if p.Name == "conformance-automation" {
			target = p
			break
		}
	}
	if target.ID == 0 {
		// The schedule fixture seeds this project. If it ever stops, say so
		// here rather than let the store refuse a schedule with no target.
		t.Fatalf("no launchable project named conformance-automation among %d; "+
			"the schedule fixture should have seeded it", len(projects))
	}

	// Everything() as the reach, for the reason the schedule fixture gives
	// for its own seed: this is seeding, not a test of the authorization
	// check, which has tests of its own.
	if _, err := schedules.Create(ctx, schedule.Schedule{
		Name:         "conformance-weekly",
		Description:  "Refreshes the automation checkout before the week starts.",
		LaunchableID: target.ID,
		Enabled:      true,
		RRule:        "FREQ=WEEKLY;BYDAY=MO;BYHOUR=6;BYMINUTE=0",
		Timezone:     "UTC",
		DTStart:      time.Date(2024, 3, 11, 6, 0, 0, 0, time.UTC),
	}, launchable.Everything()); err != nil {
		t.Fatalf("seeding the second conformance schedule: %v", err)
	}
}
