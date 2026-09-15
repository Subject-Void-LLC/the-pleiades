// Package dispatch_test: entJobStore.Cancel and the fan-out's own reaction
// to a cancel, in their own sibling file per this repository's
// file-per-concern convention (ent_store.go/ent_store_terminal.go split
// the same way).
//
// Every test here runs against the real in-memory SQLite database
// newTestStore builds, exercising the actual conditional-update idiom
// production runs, per AGENTS.md RULE 0.
package dispatch_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
)

// cancelableStates are the two states a job can really be stopped from
// today, each reached the way production reaches it rather than by writing
// the column directly: "pending" is what Create leaves behind, and
// "fanning_out" is what BeginFanOut claims.
//
// "running" is deliberately absent. Cancel accepts it, but nothing writes
// it yet (see internal/ent/schema/job.go's own State comment), so a test
// that reached it would have to forge the state by a path no caller has,
// which would assert something about this test rather than about the
// platform.
func TestEntJobStore_Cancel(t *testing.T) {
	tests := []struct {
		name string
		// claim reports whether to take the fan-out claim first, which is
		// what moves a job from "pending" to "fanning_out".
		claim bool
		want  string
	}{
		{name: "from pending", claim: false, want: "pending"},
		{name: "from fanning out", claim: true, want: "fanning_out"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			store, _ := newTestStore(t)

			job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
			if err := store.Create(ctx, job); err != nil {
				t.Fatalf("Create returned unexpected error: %v", err)
			}
			if tt.claim {
				if _, _, err := store.BeginFanOut(ctx, job.JobID, time.Hour); err != nil {
					t.Fatalf("BeginFanOut returned unexpected error: %v", err)
				}
			}

			before, _, err := store.Get(ctx, job.JobID)
			if err != nil {
				t.Fatalf("Get returned unexpected error: %v", err)
			}
			if before.State != tt.want {
				t.Fatalf("state before Cancel = %q, want %q; the test did not set up the case it names", before.State, tt.want)
			}

			// A different subject from Actor above, which is the ordinary
			// case this column exists for: whoever stops a run is very
			// often not whoever started it.
			if err := store.Cancel(ctx, job.JobID, "stopper"); err != nil {
				t.Fatalf("Cancel returned unexpected error: %v", err)
			}

			got, _, err := store.Get(ctx, job.JobID)
			if err != nil {
				t.Fatalf("Get returned unexpected error: %v", err)
			}
			if got.State != "canceled" {
				t.Errorf("State after Cancel = %q, want %q", got.State, "canceled")
			}
			if got.CanceledBy != "stopper" {
				t.Errorf("CanceledBy = %q, want %q", got.CanceledBy, "stopper")
			}
			if got.CanceledAt.IsZero() {
				t.Error("CanceledAt is the zero time, want the moment the job was stopped")
			}
			if got.Actor != "launcher" {
				t.Errorf("Actor after Cancel = %q, want the original launcher %q: cancelling must not rewrite who asked for the run", got.Actor, "launcher")
			}
		})
	}
}

// TestEntJobStore_Cancel_TerminalJobRejected proves a job that has already
// stopped cannot be stopped again, and that the refusal names the right
// reason.
//
// The distinction matters more than it looks: reporting ErrFenced here, as
// Complete and Fail's shared terminalWriteRejected would, would tell an
// operator their claim had been superseded by another worker, sending them
// to look for a second worker that does not exist. What actually happened
// is that the run finished first.
func TestEntJobStore_Cancel_TerminalJobRejected(t *testing.T) {
	tests := []struct {
		name string
		// settle drives the job to a terminal state through the real
		// terminal writer, never by setting the column.
		settle func(t *testing.T, store dispatch.JobStore, jobID string, fence int64)
		want   string
	}{
		{
			name: "already completed",
			settle: func(t *testing.T, store dispatch.JobStore, jobID string, fence int64) {
				t.Helper()
				if err := store.Complete(t.Context(), jobID, fence, 1, 0, 0); err != nil {
					t.Fatalf("Complete returned unexpected error: %v", err)
				}
			},
			want: "completed",
		},
		{
			name: "already failed",
			settle: func(t *testing.T, store dispatch.JobStore, jobID string, fence int64) {
				t.Helper()
				if err := store.Fail(t.Context(), jobID, fence, "nothing to dispatch"); err != nil {
					t.Fatalf("Fail returned unexpected error: %v", err)
				}
			},
			want: "failed",
		},
		{
			name: "already canceled",
			settle: func(t *testing.T, store dispatch.JobStore, jobID string, _ int64) {
				t.Helper()
				if err := store.Cancel(t.Context(), jobID, "first-stopper"); err != nil {
					t.Fatalf("first Cancel returned unexpected error: %v", err)
				}
			},
			want: "canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			store, _ := newTestStore(t)

			job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
			if err := store.Create(ctx, job); err != nil {
				t.Fatalf("Create returned unexpected error: %v", err)
			}
			_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
			if err != nil {
				t.Fatalf("BeginFanOut returned unexpected error: %v", err)
			}
			tt.settle(t, store, job.JobID, fence)

			err = store.Cancel(ctx, job.JobID, "late-stopper")
			if !errors.Is(err, dispatch.ErrNotCancelable) {
				t.Fatalf("Cancel of an already-%s job = %v, want a wrapped ErrNotCancelable", tt.want, err)
			}

			got, _, getErr := store.Get(ctx, job.JobID)
			if getErr != nil {
				t.Fatalf("Get returned unexpected error: %v", getErr)
			}
			if got.State != tt.want {
				t.Errorf("State after the rejected Cancel = %q, want the original %q unchanged", got.State, tt.want)
			}
			// The already-canceled case is the one that would silently
			// rewrite history if the guard were missing: a second cancel
			// would stamp the second person's name over the first's.
			if tt.want == "canceled" && got.CanceledBy != "first-stopper" {
				t.Errorf("CanceledBy after the rejected second Cancel = %q, want the first stopper %q", got.CanceledBy, "first-stopper")
			}
		})
	}
}

// TestEntJobStore_Cancel_UnknownJob proves an id naming no job is reported
// as such rather than as a job that could not be stopped, so a caller can
// answer 404 instead of 409.
func TestEntJobStore_Cancel_UnknownJob(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	err := store.Cancel(ctx, "00000000-0000-0000-0000-000000000000", "stopper")
	if !errors.Is(err, dispatch.ErrJobNotFound) {
		t.Fatalf("Cancel of an unknown job = %v, want a wrapped ErrJobNotFound", err)
	}
}

// TestEntJobStore_RecordTask_StopsOnceCanceled is the durable half of what
// Cancel promises, proven at the seam the fan-out loop actually runs
// through: once a job is canceled, the very next device the loop tries to
// record is refused, so no device the job has not already reached is ever
// dispatched to.
//
// It asserts the sentinel rather than the absence of a row, because the
// loop stops on the sentinel; a test that only counted rows would still
// pass if RecordTask silently dropped the write and let the loop carry on
// publishing dispatches.
func TestEntJobStore_RecordTask_StopsOnceCanceled(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut returned unexpected error: %v", err)
	}

	// One device recorded before the cancel, so the assertion below is
	// about the cancel rather than about RecordTask never having worked.
	first := dispatch.JobTask{DeviceID: "d-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched}
	if err := store.RecordTask(ctx, job.JobID, fence, first); err != nil {
		t.Fatalf("RecordTask before the cancel returned unexpected error: %v", err)
	}

	if err := store.Cancel(ctx, job.JobID, "stopper"); err != nil {
		t.Fatalf("Cancel returned unexpected error: %v", err)
	}

	second := dispatch.JobTask{DeviceID: "d-2", DeviceName: "router-2", Outcome: dispatch.OutcomeDispatched}
	err = store.RecordTask(ctx, job.JobID, fence, second)
	if !errors.Is(err, dispatch.ErrCanceled) {
		t.Fatalf("RecordTask after the cancel = %v, want a wrapped ErrCanceled", err)
	}

	_, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("recorded tasks after the refused write = %d, want 1: the refused device must not have been written", len(tasks))
	}
	if tasks[0].DeviceID != "d-1" {
		t.Errorf("surviving task device = %q, want the one recorded before the cancel, %q", tasks[0].DeviceID, "d-1")
	}
}

// TestEntJobStore_RecordTask_FenceBeatsCanceled pins the ORDER of
// RecordTask's two refusals, which is not arbitrary.
//
// A worker holding a stale fence is superseded whatever the job's state
// is, and another worker is legitimately running the job right now.
// Telling it the job was canceled would be false and would send whoever
// read the log to the wrong conclusion entirely. A test is the only thing
// that keeps this order, since both branches return an error and either
// ordering compiles.
func TestEntJobStore_RecordTask_FenceBeatsCanceled(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	_, stale, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut returned unexpected error: %v", err)
	}

	// A real reclaim, driven the way the Reaper drives one: a zero
	// staleAfter makes every claim immediately reclaimable, which bumps
	// the fence and leaves the first caller holding a stale one.
	claimed, fresh, err := store.BeginFanOut(ctx, job.JobID, 0)
	if err != nil {
		t.Fatalf("reclaiming BeginFanOut returned unexpected error: %v", err)
	}
	if !claimed || fresh == stale {
		t.Fatalf("reclaim did not supersede the original claim: claimed=%v fence %d -> %d", claimed, stale, fresh)
	}

	if err := store.Cancel(ctx, job.JobID, "stopper"); err != nil {
		t.Fatalf("Cancel returned unexpected error: %v", err)
	}

	// Both conditions now hold at once: the job is canceled AND this
	// caller's fence is stale. The fence is the one it must be told about.
	task := dispatch.JobTask{DeviceID: "d-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched}
	err = store.RecordTask(ctx, job.JobID, stale, task)
	if !errors.Is(err, dispatch.ErrFenced) {
		t.Fatalf("RecordTask with a stale fence against a canceled job = %v, want a wrapped ErrFenced: a superseded caller must be told it was superseded", err)
	}
	if errors.Is(err, dispatch.ErrCanceled) {
		t.Error("RecordTask reported ErrCanceled to a superseded caller, which would send it looking for a cancellation rather than for the worker that took over")
	}
}
