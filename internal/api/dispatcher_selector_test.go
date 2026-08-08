package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	_ "github.com/mattn/go-sqlite3"
)

// TestDispatcher_GroupSelector_FiltersAgainstRealRepository is the real
// end-to-end proof of a real, previously shipped bug fix: before it, a
// dispatch's "group" query parameter was silently discarded by GetGroup,
// so every dispatch hit the whole devices table regardless of which group
// a caller asked for. That fix now lives inside
// internal/inventory.entRepository.GetGroup, one layer below where the
// original version of this test exercised it: this phase moved per-device
// fan-out out of Dispatcher.DispatchRunbook entirely and into
// internal/dispatch.Worker, so the group filter's effect is no longer
// observable in DispatchRunbook's own synchronous response body. It is
// still observable, and still real, end to end: two named ent Groups are
// seeded with distinct devices, one HTTP request per group is launched
// through the real Dispatcher, a real Worker (subscribed to a real,
// in-process event.Bus) performs the fan-out asynchronously exactly as
// production does, and the test polls the real JobStore for each launch's
// final tally. One request per group must dispatch to exactly that
// group's devices, and a third request naming a group with no real
// members must dispatch to zero devices (fail closed), never fall back to
// the whole fleet.
//
// internal/dispatch/worker_test.go's own Worker tests do not cover this:
// their fakeRepository "yields exactly the devices in Devices, in order,
// from GetGroup regardless of the Selector passed in" (its own doc
// comment), which is precisely the shortcut that would hide this bug if it
// ever came back. This file exists so that real, group-filtering
// entRepository code path keeps a caller who can prove it.
func TestDispatcher_GroupSelector_FiltersAgainstRealRepository(t *testing.T) {
	// newSerializedSQLiteClient (dispatcher_testutil_test.go), not a plain
	// enttest.Open: the real Worker writes JobTask rows on its own
	// goroutine while dispatchGroup's poll loop concurrently reads from the
	// same client, which needs a capped connection pool to avoid a
	// cross-connection "database table is locked" error (see that
	// function's own doc comment for why).
	client := newSerializedSQLiteClient(t, "selector")
	ctx := context.Background()

	mkDevice := func(name string) *ent.Device {
		return client.Device.Create().
			SetName(name).
			SetType("linux_server").
			SetProperties(map[string]interface{}{"host": "10.0.0.1"}).
			SetState("active").
			SaveX(ctx)
	}

	prod1, prod2 := mkDevice("prod-1"), mkDevice("prod-2")
	staging1 := mkDevice("staging-1")

	client.Group.Create().SetName("prod-real").AddDevices(prod1, prod2).SaveX(ctx)
	client.Group.Create().SetName("staging-real").AddDevices(staging1).SaveX(ctx)

	// The same client backs both the device repository and the job store,
	// mirroring cmd/controller/main.go's own composition exactly (both
	// depend on one already-open ent.Client).
	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	jobStore := dispatch.NewEntJobStore(client)
	runbooks := newTestRunbookSource(t, "pb-1")
	bus := event.NewInProcessBus()

	worker := dispatch.NewWorker(jobStore, repo, runbooks, bus)
	if err := bus.Subscribe(ctx, topology.JobRequestedSubject(), worker.HandleJobRequested); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	dispatcher := api.NewDispatcher(runbooks, jobStore, bus)

	// dispatchGroup launches a real dispatch against groupName and blocks
	// until the resulting Job reaches a terminal state, returning it.
	dispatchGroup := func(t *testing.T, groupName string) *dispatch.Job {
		t.Helper()
		req := dispatchTestRequest(t, groupName, "pb-1")
		rr := httptest.NewRecorder()
		dispatcher.DispatchRunbook(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("dispatch to %q: expected 202 Accepted, got %v: body %s", groupName, rr.Code, rr.Body.String())
		}

		body := decodeJobAccepted(t, rr)
		if body.JobID == "" {
			t.Fatalf("dispatch to %q: response carried no job_id", groupName)
		}

		// 5 seconds is generous for three tiny groups; the 10,000-device
		// case has its own, much larger budget in
		// dispatcher_release_test.go.
		return pollJobUntilTerminal(t, ctx, jobStore, body.JobID, 5*time.Second)
	}

	if job := dispatchGroup(t, "prod-real"); job.State != "completed" || job.DispatchedCount != 2 {
		t.Errorf("dispatch to group 'prod-real': state=%q dispatched=%d, want completed/2", job.State, job.DispatchedCount)
	}

	if job := dispatchGroup(t, "staging-real"); job.State != "completed" || job.DispatchedCount != 1 {
		t.Errorf("dispatch to group 'staging-real': state=%q dispatched=%d, want completed/1", job.State, job.DispatchedCount)
	}

	// A group with zero real members (including one that does not exist
	// at all) must dispatch to zero devices, not fall back to the whole
	// fleet: the literal bug this test's own history fixed.
	if job := dispatchGroup(t, "does-not-exist"); job.State != "completed" || job.DispatchedCount != 0 {
		t.Errorf("dispatch to a nonexistent group: state=%q dispatched=%d, want completed/0 (fail closed)", job.State, job.DispatchedCount)
	}
}
