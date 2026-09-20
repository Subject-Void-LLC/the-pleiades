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
