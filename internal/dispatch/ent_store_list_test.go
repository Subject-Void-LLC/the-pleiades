package dispatch_test

import (
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
)

// These run against a real in-memory SQLite database through the same
// entJobStore production uses, per RULE 0: the ordering and the cursor
// predicate are SQL, so a double standing in for the client would be
// testing the double.

// seedJobs creates n jobs and returns their ids in creation order.
func seedJobs(t *testing.T, store dispatch.JobStore, n int) []string {
	t.Helper()
	ctx := t.Context()

	ids := make([]string, 0, n)
	for i := range n {
		job := &dispatch.Job{
			RunbookID: fmt.Sprintf("pb-%d", i),
			GroupName: "routers",
			Actor:     "user@example.com",
		}
		if err := store.Create(ctx, job); err != nil {
			t.Fatalf("Create job %d: %v", i, err)
		}
		ids = append(ids, job.JobID)
	}
	return ids
}

func TestEntJobStore_ListReturnsNewestFirst(t *testing.T) {
	store, _ := newTestStore(t)
	ids := seedJobs(t, store, 4)

	got, err := store.List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("List returned %d jobs, want %d", len(got), len(ids))
	}

	// Job ids are UUIDv7, so creation order is id order, and newest-first
	// is the reverse of the seeding order.
	for i, j := range got {
		want := ids[len(ids)-1-i]
		if j.JobID != want {
			t.Fatalf("List()[%d] = %q, want %q (newest first)", i, j.JobID, want)
		}
	}
}

func TestEntJobStore_ListPagesWithoutGapsOrRepeats(t *testing.T) {
	store, _ := newTestStore(t)
	ids := seedJobs(t, store, 5)

	var walked []string
	cursor := ""
	for range len(ids) + 1 {
		page, err := store.List(t.Context(), cursor, 2)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, j := range page {
			walked = append(walked, j.JobID)
		}
		cursor = page[len(page)-1].JobID
	}

	if len(walked) != len(ids) {
		t.Fatalf("paged walk yielded %d jobs, want %d", len(walked), len(ids))
	}
	seen := make(map[string]bool, len(walked))
	for _, id := range walked {
		if seen[id] {
			t.Fatalf("paged walk repeated job %q: %v", id, walked)
		}
		seen[id] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("paged walk never yielded job %q", id)
		}
	}
}

func TestEntJobStore_ListHonoursTheLimit(t *testing.T) {
	store, _ := newTestStore(t)
	seedJobs(t, store, 5)

	got, err := store.List(t.Context(), "", 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List with limit 2 returned %d jobs", len(got))
	}
}

// A non-positive limit is refused rather than silently treated as
// unbounded: an unbounded query over the whole job history is exactly what
// the caller-facing cap exists to prevent, and reading 0 as "everything"
// would route around it.
func TestEntJobStore_ListRefusesANonPositiveLimit(t *testing.T) {
	store, _ := newTestStore(t)

	for _, limit := range []int{0, -1} {
		if _, err := store.List(t.Context(), "", limit); err == nil {
			t.Errorf("List with limit %d = nil error, want a refusal", limit)
		}
	}
}

func TestEntJobStore_ListOnAnEmptyStoreIsEmpty(t *testing.T) {
	store, _ := newTestStore(t)

	got, err := store.List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List on an empty store returned %d jobs", len(got))
	}
}

// The list view carries no task rows. Loading them would be a query per
// job for data a list never renders.
func TestEntJobStore_ListCarriesNoTasks(t *testing.T) {
	store, _ := newTestStore(t)
	ids := seedJobs(t, store, 1)

	claimed, fence, err := store.BeginFanOut(t.Context(), ids[0], 0)
	if err != nil || !claimed {
		t.Fatalf("BeginFanOut = (%v, %d, %v)", claimed, fence, err)
	}
	if err := store.RecordTask(t.Context(), ids[0], fence, dispatch.JobTask{
		DeviceID: "d-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched,
	}); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}

	// Get carries the task; List is the same job without it.
	if _, tasks, err := store.Get(t.Context(), ids[0]); err != nil || len(tasks) != 1 {
		t.Fatalf("Get = (%d tasks, %v), want 1 task", len(tasks), err)
	}
	got, err := store.List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d jobs, want 1", len(got))
	}
	if got[0].JobID != ids[0] {
		t.Errorf("List returned job %q, want %q", got[0].JobID, ids[0])
	}
}
