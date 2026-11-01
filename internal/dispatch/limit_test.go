// Tests of a job's limit in the Controller's fan-out: only the devices it
// admits by name, tag or group (ancestors included) are part of the job, and
// a limit that admits none, or cannot be read, fails the job.
package dispatch_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// groupedRepository is namedRepository that also answers group names, as the
// ent repository does (inventory.GroupNamer).
type groupedRepository struct {
	*namedRepository
	groups map[pkginventory.DeviceID][]string
}

// DeviceGroupNames implements inventory.GroupNamer.
func (r *groupedRepository) DeviceGroupNames(context.Context) (map[pkginventory.DeviceID][]string, error) {
	return r.groups, nil
}

// limitFixture fans out a job over five devices (id-i named dev-i, dev-2 and
// dev-3 in group dc, dev-3 also in its child group rack-7, given here with
// the ancestor as GroupNamer reports it) with the launch's limit.
func limitFixture(t *testing.T, limit string) *windowFixture {
	t.Helper()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	named := &namedRepository{}
	for i := 0; i < 5; i++ {
		named.Devices = append(named.Devices, capableDevice("id-"+string(rune('0'+i)), "dev-"+string(rune('0'+i)), "10.0.0.1"))
	}
	repo := &groupedRepository{namedRepository: named, groups: map[pkginventory.DeviceID][]string{
		"id-2": {"dc"}, "id-3": {"dc", "rack-7"},
	}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)
	evt := requestJobWithLaunchFields(t, t.Context(), store, "pb-1", "routers", launch.Fields{dispatch.LimitField: limit}, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	return &windowFixture{store: store, bus: bus, worker: worker, jobID: jobIDFromEvent(t, evt)}
}

// TestLimit_OnlyWhatItAdmitsIsPartOfTheJob: names, a glob with an exclusion,
// a group, and a group intersected with a child group each dispatch only the
// devices they name, and the rest have no row.
func TestLimit_OnlyWhatItAdmitsIsPartOfTheJob(t *testing.T) {
	for limit, want := range map[string][]string{
		"dev-1,dev-4":    {"id-1", "id-4"},
		"dev-*,!dev-0":   {"id-1", "id-2", "id-3", "id-4"},
		"dc":             {"id-2", "id-3"},
		"dc,&rack-7":     {"id-3"},
		"all,!dc,!dev-4": {"id-0", "id-1"},
	} {
		f := limitFixture(t, limit)
		got := f.dispatchedDevices(t)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("limit %q dispatched %v, want %v", limit, got, want)
		}
		_, tasks, err := f.store.Get(t.Context(), f.jobID)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != len(want) {
			t.Errorf("limit %q left %d rows, want one per admitted device (%d)", limit, len(tasks), len(want))
		}
	}
}

// TestLimit_ALimitThatAdmitsNothingOrCannotBeReadFailsTheJob.
func TestLimit_ALimitThatAdmitsNothingOrCannotBeReadFailsTheJob(t *testing.T) {
	for limit, want := range map[string]string{
		"core-*": `the launch's limit "core-*" matches none of this job's devices`,
		"~dev.*": "regular expression",
	} {
		f := limitFixture(t, limit)
		job, err := f.store.Lookup(t.Context(), f.jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "failed" || !strings.Contains(job.FailureReason, want) {
			t.Errorf("limit %q: job %s %q, want failed saying %q", limit, job.State, job.FailureReason, want)
		}
		if got := f.dispatchedDevices(t); len(got) != 0 {
			t.Errorf("limit %q dispatched %v", limit, got)
		}
	}
}
