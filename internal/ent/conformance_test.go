// This file is the adapter conformance suite: one set of assertions run
// identically against every dialect OpenDatabase supports.
//
// It deliberately covers what actually differs between dialects rather
// than re-testing ent. Enum columns, JSON columns, unique constraints,
// the many-to-many join the dispatcher's group targeting depends on,
// cascade deletes, and the affected-row semantics of a conditional UPDATE
// are each a place where SQLite and PostgreSQL could legitimately behave
// differently, and each is something this project's correctness rests on.
package ent_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/group"
)

// openConformanceClient opens a client against a fresh database for the
// given backend and closes it when the test ends.
func openConformanceClient(t *testing.T, backend storeBackend) (*ent.Client, string) {
	t.Helper()
	dsn := backend.newDSN(t)
	client, err := ent.OpenDatabase(context.Background(), ent.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("OpenDatabase(%s): %v", backend.name, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, dsn
}

// TestConformance_OpenIsIdempotent proves opening the same database twice
// is a clean no-op rather than an error or a second migration run.
//
// This is migrate.Apply's stated guarantee, and it is exactly where a
// dialect-specific mistake in the version-record statement surfaces: the
// first open writes the schema_migrations rows, and the second is the
// only thing that reads them back and decides there is nothing to do.
func TestConformance_OpenIsIdempotent(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			dsn := backend.newDSN(t)
			ctx := context.Background()

			first, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
			if err != nil {
				t.Fatalf("first OpenDatabase: %v", err)
			}
			if err := first.Close(); err != nil {
				t.Fatalf("closing the first client: %v", err)
			}

			second, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
			if err != nil {
				t.Fatalf("second OpenDatabase against an already-migrated database: %v", err)
			}
			defer second.Close()

			// The reopened client must be usable, not merely constructed.
			if _, err := second.Device.Query().Count(ctx); err != nil {
				t.Fatalf("querying through the reopened client: %v", err)
			}
		})
	}
}

// TestConformance_DeviceRoundTrip proves every column type the Device
// schema uses survives a write and a read on this dialect.
//
// The JSON columns (properties, tags) are the interesting ones: SQLite
// stores them as text and PostgreSQL as jsonb, which are genuinely
// different storage engines for the same Go value.
func TestConformance_DeviceRoundTrip(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			properties := map[string]interface{}{
				"host":       "10.0.0.1",
				"port":       float64(2222),
				"enabled":    true,
				"interfaces": []interface{}{"Gi0/0", "Gi0/1"},
			}
			created := client.Device.Create().
				SetName("rtr1").
				SetType("cisco_router").
				SetProperties(properties).
				SetTags([]string{"edge", "production"}).
				SaveX(ctx)

			read := client.Device.GetX(ctx, created.ID)

			if read.Name != "rtr1" || read.Type != "cisco_router" {
				t.Fatalf("name/type round trip failed: got %q/%q", read.Name, read.Type)
			}
			// The schema default, not a value this test set.
			if read.State != "active" {
				t.Fatalf("state default = %q, want %q", read.State, "active")
			}
			if read.DeviceID == "" {
				t.Fatal("device_id was not populated by its DefaultFunc")
			}
			if got := read.Properties["host"]; got != "10.0.0.1" {
				t.Fatalf("properties[host] = %v, want 10.0.0.1", got)
			}
			if got := read.Properties["port"]; got != float64(2222) {
				t.Fatalf("properties[port] = %#v, want float64(2222); a dialect that reshapes JSON numbers would break capability resolution", got)
			}
			if got := read.Properties["enabled"]; got != true {
				t.Fatalf("properties[enabled] = %#v, want true", got)
			}
			if len(read.Tags) != 2 || read.Tags[0] != "edge" || read.Tags[1] != "production" {
				t.Fatalf("tags round trip failed: %#v", read.Tags)
			}
			if read.CreatedAt.IsZero() || read.UpdatedAt.IsZero() {
				t.Fatal("the timestamp mixin left created_at or updated_at zero")
			}
		})
	}
}

// TestConformance_DeviceNameIsUnique proves the unique index on
// devices.name is really enforced by this dialect's schema.
//
// A migration that silently dropped a unique index would otherwise be
// invisible until two devices with one name broke inventory lookups.
func TestConformance_DeviceNameIsUnique(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			client.Device.Create().SetName("rtr1").SetType("cisco_router").SaveX(ctx)

			_, err := client.Device.Create().SetName("rtr1").SetType("cisco_router").Save(ctx)
			if err == nil {
				t.Fatal("a second device with a duplicate name was accepted, so the unique index is not enforced")
			}
		})
	}
}

// TestConformance_GroupMembershipPredicate proves the exact predicate the
// dispatcher's group targeting uses returns exactly the group's members.
//
// internal/inventory's entRepository.GetGroup filters with
// device.HasGroupsWith(group.NameEQ(...)), which compiles to an EXISTS
// subquery over the group_devices join table. If that behaved differently
// here than on the other dialect, a dispatch would fan out to the wrong
// devices, which is the single most consequential thing this schema does.
func TestConformance_GroupMembershipPredicate(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			edge1 := client.Device.Create().SetName("rtr1").SetType("cisco_router").SaveX(ctx)
			edge2 := client.Device.Create().SetName("rtr2").SetType("cisco_router").SaveX(ctx)
			core1 := client.Device.Create().SetName("rtr3").SetType("cisco_router").SaveX(ctx)
			// A device in no group at all, which must never be selected.
			client.Device.Create().SetName("rtr4").SetType("cisco_router").SaveX(ctx)

			client.Group.Create().SetName("edge").AddDevices(edge1, edge2).SaveX(ctx)
			client.Group.Create().SetName("core").AddDevices(core1).SaveX(ctx)

			selected := client.Device.Query().
				Where(device.HasGroupsWith(group.NameEQ("edge"))).
				Order(device.ByName()).
				AllX(ctx)

			if len(selected) != 2 {
				t.Fatalf("group edge selected %d devices, want 2", len(selected))
			}
			if selected[0].Name != "rtr1" || selected[1].Name != "rtr2" {
				t.Fatalf("group edge selected %q and %q, want rtr1 and rtr2", selected[0].Name, selected[1].Name)
			}

			// An unknown group must fail closed to zero devices, never to
			// every device.
			none := client.Device.Query().
				Where(device.HasGroupsWith(group.NameEQ("does-not-exist"))).
				AllX(ctx)
			if len(none) != 0 {
				t.Fatalf("an unknown group selected %d devices, want 0; a selector that fails open would dispatch to the whole fleet", len(none))
			}
		})
	}
}

// TestConformance_DeletingAGroupLeavesItsDevices proves the join table's
// cascade really made it into this dialect's DDL: removing a group must
// remove its membership rows and leave the devices themselves alone.
func TestConformance_DeletingAGroupLeavesItsDevices(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			d1 := client.Device.Create().SetName("rtr1").SetType("cisco_router").SaveX(ctx)
			g := client.Group.Create().SetName("edge").AddDevices(d1).SaveX(ctx)

			client.Group.DeleteOne(g).ExecX(ctx)

			if count := client.Device.Query().CountX(ctx); count != 1 {
				t.Fatalf("deleting a group left %d devices, want 1; the cascade reached the devices themselves", count)
			}
			orphaned := client.Device.Query().
				Where(device.HasGroupsWith(group.NameEQ("edge"))).
				CountX(ctx)
			if orphaned != 0 {
				t.Fatalf("deleting a group left %d membership rows behind", orphaned)
			}
		})
	}
}

// TestConformance_JobFencing proves the conditional-UPDATE semantics the
// dispatcher's fan-out correctness rests on.
//
// BeginFanOut claims a job by moving it out of the pending state and
// bumping a fencing token, and it decides whether it won by counting the
// rows its conditional UPDATE affected. A dialect that reported that
// count differently would let two workers both believe they own one job,
// which is the failure this token exists to prevent.
//
// It lives here rather than in internal/dispatch's own tests so that
// internal/dispatch stays container free while still having its most
// dialect-sensitive behavior proven on both backends.
func TestConformance_JobFencing(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			store := dispatch.NewEntJobStore(client)
			job := &dispatch.Job{RunbookID: "ping", GroupName: "edge", Actor: "conformance"}
			if err := store.Create(ctx, job); err != nil {
				t.Fatalf("creating a job: %v", err)
			}

			claimed, fence, err := store.BeginFanOut(ctx, job.JobID, dispatch.DefaultFanOutLeaseTTL)
			if err != nil {
				t.Fatalf("first BeginFanOut: %v", err)
			}
			if !claimed {
				t.Fatal("the first BeginFanOut did not claim a pending job")
			}

			// A second claim against a job already being fanned out, and
			// well inside its lease, must lose.
			reclaimed, _, err := store.BeginFanOut(ctx, job.JobID, dispatch.DefaultFanOutLeaseTTL)
			if err != nil {
				t.Fatalf("second BeginFanOut: %v", err)
			}
			if reclaimed {
				t.Fatal("a second BeginFanOut claimed a job already being fanned out, so two workers would both fan it out")
			}

			// A write carrying a stale fence must be rejected.
			err = store.RecordTask(ctx, job.JobID, fence-1, dispatch.JobTask{
				DeviceID:   "device-1",
				DeviceName: "rtr1",
				Outcome:    dispatch.OutcomeDispatched,
			})
			if !errors.Is(err, dispatch.ErrFenced) {
				t.Fatalf("RecordTask with a stale fence returned %v, want ErrFenced", err)
			}

			// The current fence must still be accepted, so the guard
			// rejects staleness rather than everything.
			if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
				DeviceID:   "device-1",
				DeviceName: "rtr1",
				Outcome:    dispatch.OutcomeDispatched,
			}); err != nil {
				t.Fatalf("RecordTask with the current fence: %v", err)
			}
		})
	}
}

// TestConformance_JobEnumsRoundTrip proves both enum columns survive a
// write and a read on this dialect.
//
// ent renders these as varchar with a check rather than as a native
// PostgreSQL enum type, which is what makes them portable; this test is
// what would notice if that ever changed.
func TestConformance_JobEnumsRoundTrip(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			store := dispatch.NewEntJobStore(client)
			job := &dispatch.Job{RunbookID: "ping", GroupName: "edge", Actor: "conformance"}
			if err := store.Create(ctx, job); err != nil {
				t.Fatalf("creating a job: %v", err)
			}
			if job.State != "pending" {
				t.Fatalf("a new job's state = %q, want the schema default %q", job.State, "pending")
			}

			_, fence, err := store.BeginFanOut(ctx, job.JobID, dispatch.DefaultFanOutLeaseTTL)
			if err != nil {
				t.Fatalf("BeginFanOut: %v", err)
			}

			// One task per outcome, so every value of the enum is
			// actually written rather than just the first.
			outcomes := []dispatch.Outcome{
				dispatch.OutcomeDispatched,
				dispatch.OutcomeSkipped,
				dispatch.OutcomeFailed,
			}
			for i, outcome := range outcomes {
				if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
					DeviceID:   fmt.Sprintf("device-%d", i),
					DeviceName: fmt.Sprintf("rtr%d", i),
					Outcome:    outcome,
					Reason:     string(outcome) + " reason",
				}); err != nil {
					t.Fatalf("RecordTask(%s): %v", outcome, err)
				}
			}

			if err := store.Complete(ctx, job.JobID, fence, 1, 1, 1); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			read, tasks, err := store.Get(ctx, job.JobID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if read.State != "completed" {
				t.Fatalf("state = %q, want completed", read.State)
			}
			if len(tasks) != len(outcomes) {
				t.Fatalf("got %d tasks, want %d", len(tasks), len(outcomes))
			}
			seen := make(map[dispatch.Outcome]bool, len(tasks))
			for _, task := range tasks {
				seen[task.Outcome] = true
			}
			for _, outcome := range outcomes {
				if !seen[outcome] {
					t.Fatalf("outcome %q did not survive the round trip", outcome)
				}
			}
		})
	}
}

// TestConformance_ConcurrentWrites proves the dialect tolerates several
// writers at once through one pool.
//
// On SQLite this exercises the busy timeout the adapter sets, and on
// PostgreSQL it exercises the pool itself. Either way, the failure this
// catches is a write that silently does not happen.
func TestConformance_ConcurrentWrites(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			const writers = 8
			errs := make(chan error, writers)
			var wg sync.WaitGroup
			for i := 0; i < writers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := client.Device.Create().
						SetName(fmt.Sprintf("rtr%d", i)).
						SetType("cisco_router").
						Save(ctx)
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)

			for err := range errs {
				if err != nil {
					t.Fatalf("a concurrent write failed: %v", err)
				}
			}
			if count := client.Device.Query().CountX(ctx); count != writers {
				t.Fatalf("after %d concurrent writes the table holds %d rows", writers, count)
			}
		})
	}
}
