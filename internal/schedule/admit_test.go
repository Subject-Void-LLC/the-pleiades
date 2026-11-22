// Package schedule_test's admission coverage: what a store does with each way
// a target can be unacceptable.
//
// These go through the real store rather than calling the mapping functions
// directly, because what matters is not that a function returns a FieldError
// but that a refusal arrives at the write attached to the control that caused
// it. A message with nowhere to land renders as a form-wide failure saying
// nothing about what to change, which is the same as no message.
//
// The admitter is faked here, and only here. Every other test in this package
// uses the real one over a real database; this file exists for the paths a real
// admitter cannot be made to take on demand: a launcher's own objection (the
// real one has no launchers wired), a storage failure reading the target, and a
// row whose type this build does not know.
package schedule_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
)

// newAdmitClient opens a real database for these tests. The store is real
// throughout; only the admitter is faked.
func newAdmitClient(t *testing.T) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s/admit.db?_fk=1", t.TempDir()))
	t.Cleanup(func() { _ = client.Close() })
	client.Organization.Create().SetName("network").SaveX(context.Background())
	return client
}

// fakeAdmitter answers the two questions a schedule's store asks about what it
// is being pointed at.
type fakeAdmitter struct {
	target       launchable.Target
	getErr       error
	preflightErr error
}

func (f fakeAdmitter) Get(context.Context, int) (launchable.Target, error) {
	if f.getErr != nil {
		return launchable.Target{}, f.getErr
	}
	return f.target, nil
}

func (f fakeAdmitter) Preflight(context.Context, launchable.Request) error {
	return f.preflightErr
}

// admittedTarget is a launchable a caller may legitimately point at, so a test
// varying one thing does not fail on another.
func admittedTarget() launchable.Target {
	return launchable.Target{
		ID: 44, Type: launchable.TypeJobTemplate,
		Name: "patch the edge", OrganizationID: 1,
	}
}

// scheduleFor builds a valid schedule naming one launchable.
func scheduleFor(target launchable.Target) schedule.Schedule {
	return schedule.Schedule{
		Name:         "nightly",
		LaunchableID: target.ID,
		Enabled:      true,
		RRule:        "FREQ=DAILY",
		Timezone:     "UTC",
		DTStart:      time.Date(2024, 3, 8, 9, 0, 0, 0, time.UTC),
	}
}

// TestCreate_ATypesOwnObjectionLandsOnTheControlItNames covers the refusals a
// launcher makes about its own kind of target, which are the ones that would
// otherwise surface unattended at whatever hour the schedule named.
//
// The field matters and is the launcher's to choose: it knows whether the
// problem is the thing being launched or the configuration chosen for it, and
// this package does not.
func TestCreate_ATypesOwnObjectionLandsOnTheControlItNames(t *testing.T) {
	client := newAdmitClient(t)

	cases := []struct {
		name      string
		objection error
		wantField string
	}{
		{
			name: "a refusal naming its own field keeps it",
			objection: launchable.Refusal{
				Field:   launchable.SavedConfigField,
				Message: "that saved configuration answers a survey password",
			},
			wantField: launchable.SavedConfigField,
		},
		{
			name: "a refusal naming no field blames what is being launched",
			objection: launchable.Refusal{
				Message: "this project has no fetchable source",
			},
			wantField: schedule.TargetField,
		},
		{
			// A launcher failing for a reason it did not classify is still a
			// refusal somebody has to read, so it lands on the target rather
			// than escaping as a storage error.
			name:      "a plain error blames what is being launched",
			objection: errors.New("the dispatcher is not wired on this controller"),
			wantField: schedule.TargetField,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := schedule.NewEntStore(client, fakeAdmitter{
				target:       admittedTarget(),
				preflightErr: tc.objection,
			})

			_, err := store.Create(context.Background(), scheduleFor(admittedTarget()), launchable.Everything())
			var fe schedule.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Create() = %v, want a FieldError naming a control", err)
			}
			if fe.Field != tc.wantField {
				t.Errorf("the refusal blames %q, want %q", fe.Field, tc.wantField)
			}
			if fe.Message == "" {
				t.Error("the refusal carries no message, so a form would render an empty error")
			}
		})
	}
}

// TestCreate_ARowOfAnUnknownTypeIsRefusedRatherThanLaunched covers a row
// written by a newer version, or by hand: the type is a string in the database
// and nothing constrains it to what this build registered.
//
// Refused rather than defaulted, because every default available is wrong: a
// launch of the wrong sort of thing is worse than no launch.
func TestCreate_ARowOfAnUnknownTypeIsRefusedRatherThanLaunched(t *testing.T) {
	client := newAdmitClient(t)

	unknown := admittedTarget()
	unknown.Type = "terraform_workspace"
	store := schedule.NewEntStore(client, fakeAdmitter{target: unknown})

	_, err := store.Create(context.Background(), scheduleFor(unknown), launchable.Everything())
	if !errors.Is(err, launchable.ErrUnknownType) {
		t.Fatalf("Create() = %v, want ErrUnknownType", err)
	}
	var fe schedule.FieldError
	if !errors.As(err, &fe) || fe.Field != schedule.TargetField {
		t.Errorf("the refusal does not blame what is being launched: %v", err)
	}
}

// TestCreate_AStorageFailureReadingTheTargetIsNotAFieldError proves the two
// kinds of failure stay apart: a target that cannot be read because the
// database is broken is not a message to put under a control, because there is
// nothing the person filling in the form can do about it.
func TestCreate_AStorageFailureReadingTheTargetIsNotAFieldError(t *testing.T) {
	client := newAdmitClient(t)

	broken := errors.New("reading launchable 44: database is locked")
	store := schedule.NewEntStore(client, fakeAdmitter{getErr: broken})

	_, err := store.Create(context.Background(), scheduleFor(admittedTarget()), launchable.Everything())
	if !errors.Is(err, broken) {
		t.Fatalf("Create() = %v, want the storage failure itself", err)
	}
	var fe schedule.FieldError
	if errors.As(err, &fe) {
		t.Errorf("a storage failure was reported as a field error on %q", fe.Field)
	}
}

// TestNewEntStore_RefusesANilAdmitter covers the constructor's refusal.
//
// A store that admitted anything would re-open the hole where writing a
// schedule needed no permission to launch what it launches, and would accept
// schedules that can only ever fail. A panic at composition is the cheapest
// possible place to find that out; the alternative is a Controller that runs
// for months with the check silently absent.
func TestNewEntStore_RefusesANilAdmitter(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewEntStore accepted a nil Admitter, so every check on what a schedule launches would be skipped")
		}
	}()
	_ = schedule.NewEntStore(newAdmitClient(t), nil)
}

// standingAdmitter is a real target for every id and records, for each
// Preflight, whether the store said the write leaves standing what already
// fires.
type standingAdmitter struct {
	standing *[]bool
}

func (a standingAdmitter) Get(_ context.Context, id int) (launchable.Target, error) {
	target := admittedTarget()
	target.ID = id
	return target, nil
}

func (a standingAdmitter) Preflight(_ context.Context, req launchable.Request) error {
	*a.standing = append(*a.standing, req.Standing)
	return nil
}

// TestUpdate_TellsTheTypeWhetherTheEditLeavesStandingWhatFires: a create, an
// edit that points the schedule somewhere else and an edit that turns it on
// are writes a type's every objection applies to. An edit that keeps the
// target and does not turn the schedule on is told to the type as standing
// (launchable.Request.Standing), which is what lets a schedule whose target
// broke still be renamed or paused.
func TestUpdate_TellsTheTypeWhetherTheEditLeavesStandingWhatFires(t *testing.T) {
	client := newAdmitClient(t)
	ctx := context.Background()
	org := client.Organization.Query().OnlyX(ctx)
	inv := client.Inventory.Create().SetName("edge").SetOrganization(org).SaveX(ctx)
	// A launchable row stands for exactly one template, as the template store
	// writes it.
	launchableFor := func(name string) *ent.Launchable {
		tmpl := client.Template.Create().SetName(name).SetKind("runbook").SetDefinition(name).
			SetOrganization(org).SetInventory(inv).SaveX(ctx)
		return client.Launchable.Create().SetType(launchable.TypeJobTemplate).SetName(name).
			SetOrganization(org).SetTemplate(tmpl).SaveX(ctx)
	}
	first, second := launchableFor("first"), launchableFor("second")

	var standing []bool
	store := schedule.NewEntStore(client, standingAdmitter{standing: &standing})

	sched := scheduleFor(launchable.Target{ID: first.ID})
	sched.OrganizationID = org.ID
	created, err := store.Create(ctx, sched, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		name string
		edit func(*schedule.Schedule)
		want bool
	}{
		{"a rename", func(s *schedule.Schedule) { s.Name = "nightly, renamed" }, true},
		{"a pause", func(s *schedule.Schedule) { s.Enabled = false }, true},
		{"an edit while paused", func(s *schedule.Schedule) { s.Description = "paused for now" }, true},
		{"turning it back on", func(s *schedule.Schedule) { s.Enabled = true }, false},
		{"pointing it somewhere else", func(s *schedule.Schedule) { s.LaunchableID = second.ID }, false},
	}
	want := []bool{false}
	current := created
	for _, step := range steps {
		step.edit(&current)
		updated, err := store.Update(ctx, current, launchable.Everything())
		if err != nil {
			t.Fatalf("%s: Update = %v", step.name, err)
		}
		current = updated
		want = append(want, step.want)
		if got := standing[len(standing)-1]; got != step.want {
			t.Errorf("%s: the type was told standing = %v, want %v", step.name, got, step.want)
		}
	}
	if len(standing) != len(want) || standing[0] {
		t.Errorf("Preflight was told %v, want %v (a create is never standing)", standing, want)
	}
}

// pausingAdmitter is standingAdmitter with another writer in it: during its
// first Preflight, which runs between the store's read of the schedule and
// its write, it pauses the schedule, as a second request would.
//
// With refuseEnable it objects to every write that is not standing, as a job
// template whose runbook does not compile does.
type pausingAdmitter struct {
	standingAdmitter
	pause        func()
	done         *bool
	refuseEnable bool
}

func (a pausingAdmitter) Preflight(ctx context.Context, req launchable.Request) error {
	if !*a.done {
		*a.done = true
		a.pause()
	}
	if a.refuseEnable && !req.Standing {
		return launchable.Refusal{Field: launchable.TargetField, Message: "its runbook does not compile"}
	}
	return a.standingAdmitter.Preflight(ctx, req)
}

// TestUpdate_AConcurrentPauseDoesNotMakeAnEnableLookStanding: an edit that
// keeps a schedule on is judged standing from the row as it was read. When
// another write pauses the schedule before the edit is written, the edit
// would turn it back on, so it is not written over the pause: it is judged
// again from the stored row, and the type is asked as for any enable. A type
// that objects then refuses it and the schedule stays paused.
func TestUpdate_AConcurrentPauseDoesNotMakeAnEnableLookStanding(t *testing.T) {
	client := newAdmitClient(t)
	ctx := context.Background()
	org := client.Organization.Query().OnlyX(ctx)
	inv := client.Inventory.Create().SetName("edge").SetOrganization(org).SaveX(ctx)
	tmpl := client.Template.Create().SetName("first").SetKind("runbook").SetDefinition("first").
		SetOrganization(org).SetInventory(inv).SaveX(ctx)
	target := client.Launchable.Create().SetType(launchable.TypeJobTemplate).SetName("first").
		SetOrganization(org).SetTemplate(tmpl).SaveX(ctx)

	var standing []bool
	plain := schedule.NewEntStore(client, standingAdmitter{standing: &standing})
	sched := scheduleFor(launchable.Target{ID: target.ID})
	sched.OrganizationID = org.ID
	created, err := plain.Create(ctx, sched, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}

	standing = nil
	paused := false
	racing := schedule.NewEntStore(client, pausingAdmitter{
		standingAdmitter: standingAdmitter{standing: &standing},
		done:             &paused,
		pause: func() {
			client.Schedule.Update().SetEnabled(false).ExecX(ctx)
		},
	})
	edit := created
	edit.Name = "nightly, renamed"
	updated, err := racing.Update(ctx, edit, launchable.Everything())
	if err != nil {
		t.Fatalf("Update = %v", err)
	}
	if len(standing) != 2 || !standing[0] || standing[1] {
		t.Errorf("the type was told standing = %v, want [true false]: judged again, as an enable, once the pause was seen", standing)
	}
	if !updated.Enabled || updated.Name != "nightly, renamed" {
		t.Errorf("the edit, admitted as an enable, wrote %+v", updated)
	}

	// The same race against a type that objects to an enable: refused, and the
	// pause stands with nothing of the edit written.
	paused = false
	refusing := schedule.NewEntStore(client, pausingAdmitter{
		standingAdmitter: standingAdmitter{standing: &standing},
		done:             &paused,
		pause:            func() { client.Schedule.Update().SetEnabled(false).ExecX(ctx) },
		refuseEnable:     true,
	})
	edit = updated
	edit.Name = "nightly, renamed again"
	var refused schedule.FieldError
	if _, err := refusing.Update(ctx, edit, launchable.Everything()); !errors.As(err, &refused) || refused.Field != schedule.TargetField {
		t.Fatalf("Update over a concurrent pause, by a type that objects to an enable = %v, want its refusal", err)
	}
	stored, err := plain.Get(ctx, org.ID, created.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled || stored.Name != "nightly, renamed" {
		t.Errorf("after the refusal the schedule reads enabled %v, name %q; want it paused and unchanged", stored.Enabled, stored.Name)
	}
}

// meddlingAdmitter approves everything and runs meddle during each Preflight,
// which is between the store's read of a schedule and its write.
type meddlingAdmitter struct {
	meddle func()
}

func (a meddlingAdmitter) Get(_ context.Context, id int) (launchable.Target, error) {
	target := admittedTarget()
	target.ID = id
	return target, nil
}

func (a meddlingAdmitter) Preflight(context.Context, launchable.Request) error {
	a.meddle()
	return nil
}

// TestUpdate_AStandingEditIsHeldToTheRowItWasJudgedAgainst covers the rest of
// that rule: a standing edit of a schedule with a saved configuration is
// written (the configuration is part of what was judged), an edit that finds
// the row changed every time gives up with an error and writes nothing, and
// an edit that is not standing reports a schedule deleted under it as gone.
func TestUpdate_AStandingEditIsHeldToTheRowItWasJudgedAgainst(t *testing.T) {
	client := newAdmitClient(t)
	ctx := context.Background()
	org := client.Organization.Query().OnlyX(ctx)
	inv := client.Inventory.Create().SetName("edge").SetOrganization(org).SaveX(ctx)
	launchableFor := func(name string) (*ent.Template, *ent.Launchable) {
		tmpl := client.Template.Create().SetName(name).SetKind("runbook").SetDefinition(name).
			SetOrganization(org).SetInventory(inv).SaveX(ctx)
		return tmpl, client.Launchable.Create().SetType(launchable.TypeJobTemplate).SetName(name).
			SetOrganization(org).SetTemplate(tmpl).SaveX(ctx)
	}
	tmpl, first := launchableFor("first")
	_, second := launchableFor("second")
	cfg := client.SavedLaunchConfig.Create().SetName("core only").
		SetFields(map[string]any{"limit": "core-*"}).SetTemplate(tmpl).SaveX(ctx)

	meddle := func() {}
	store := schedule.NewEntStore(client, meddlingAdmitter{meddle: func() { meddle() }})
	sched := scheduleFor(launchable.Target{ID: first.ID})
	sched.OrganizationID = org.ID
	sched.SavedConfigID = cfg.ID
	created, err := store.Create(ctx, sched, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}

	// A standing edit of a schedule that has a saved configuration.
	edit := created
	edit.Name = "nightly, renamed"
	renamed, err := store.Update(ctx, edit, launchable.Everything())
	if err != nil || renamed.Name != "nightly, renamed" || renamed.SavedConfigID != cfg.ID {
		t.Fatalf("a standing edit keeping its saved configuration = %+v, %v", renamed, err)
	}

	// Another writer flips the schedule on and off during every attempt, so no
	// attempt's write finds the row it was judged against.
	flips := 0
	meddle = func() {
		flips++
		client.Schedule.Update().SetEnabled(flips%2 == 0).ExecX(ctx)
	}
	edit = renamed
	edit.Enabled = false
	edit.Name = "never written"
	if _, err := store.Update(ctx, edit, launchable.Everything()); err == nil {
		t.Error("an edit whose row changed under every attempt was written")
	}
	if flips < 2 {
		t.Errorf("the edit was judged %d times, want it judged again after the row changed", flips)
	}
	if got := client.Schedule.Query().OnlyX(ctx); got.Name != "nightly, renamed" {
		t.Errorf("the abandoned edit wrote the name %q", got.Name)
	}

	// An edit that is not standing (it points the schedule elsewhere), whose
	// schedule is deleted before it is written.
	meddle = func() { client.Schedule.Delete().ExecX(ctx) }
	edit = renamed
	edit.LaunchableID = second.ID
	edit.SavedConfigID = 0
	if _, err := store.Update(ctx, edit, launchable.Everything()); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("an edit of a schedule deleted under it = %v, want ErrNotFound", err)
	}
}
