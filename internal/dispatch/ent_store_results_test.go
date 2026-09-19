// Package dispatch_test: result aggregation, the half of a job's life that
// happens after its fan-out ends.
//
// Everything here runs against the real in-memory SQLite database
// newTestStore builds, exercising the actual conditional writes production
// runs, per AGENTS.md RULE 0.
package dispatch_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
)

// runningJob creates a job, claims its fan-out, records one dispatched
// task per device, and settles it into "running", which is the state a
// real fan-out leaves behind when it handed work to a Runner.
func runningJob(t *testing.T, store dispatch.JobStore, deviceIDs ...string) (string, int64) {
	t.Helper()
	ctx := t.Context()

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	for _, id := range deviceIDs {
		if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
			DeviceID:   id,
			DeviceName: id,
			Outcome:    dispatch.OutcomeDispatched,
		}); err != nil {
			t.Fatalf("RecordTask(%s): %v", id, err)
		}
	}
	if err := store.SettleRunning(ctx, job.JobID, fence, len(deviceIDs), 0, 0); err != nil {
		t.Fatalf("SettleRunning: %v", err)
	}
	return job.JobID, fence
}

// TestEntJobStore_SettleRunning proves a fan-out that dispatched something
// ends in "running" with its tallies stamped.
func TestEntJobStore_SettleRunning(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1", "dev-2")

	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "running" {
		t.Errorf("State = %q, want %q", got.State, "running")
	}
	if got.DispatchedCount != 2 {
		t.Errorf("DispatchedCount = %d, want 2", got.DispatchedCount)
	}
}

// TestEntJobStore_SettleRunning_RefusesACanceledJob is the interaction
// that would otherwise revive a job somebody stopped.
//
// A cancel does not wait for the fan-out loop to notice, so the loop can
// reach its own ending a moment later. Without the state guard it would
// drag the job back out of "canceled" into "running", where it would then
// wait forever for devices that are never coming.
func TestEntJobStore_SettleRunning_RefusesACanceledJob(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	if err := store.Cancel(ctx, job.JobID, "operator"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if err := store.SettleRunning(ctx, job.JobID, fence, 1, 0, 0); err == nil {
		t.Fatal("SettleRunning on a canceled job returned no error, so a cancel could be undone by a late fan-out")
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "canceled" {
		t.Errorf("State = %q, want %q: a canceled job must stay canceled", got.State, "canceled")
	}
}

// TestEntJobStore_RecordResult_EndsTheJobOnTheLastDevice walks the whole
// aggregation: a result for one of two devices leaves the job waiting, and
// the second ends it.
func TestEntJobStore_RecordResult_EndsTheJobOnTheLastDevice(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1", "dev-2")

	complete, err := store.RecordResult(ctx, jobID, "dev-1", dispatch.ResultSucceeded, "", 0)
	if err != nil {
		t.Fatalf("RecordResult(dev-1): %v", err)
	}
	if complete {
		t.Fatal("the job reported complete with one of two devices still outstanding")
	}

	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "running" {
		t.Errorf("State with one device outstanding = %q, want %q", got.State, "running")
	}

	complete, err = store.RecordResult(ctx, jobID, "dev-2", dispatch.ResultFailed, "the play did not converge", 0)
	if err != nil {
		t.Fatalf("RecordResult(dev-2): %v", err)
	}
	if !complete {
		t.Fatal("the job did not report complete after its last device reported")
	}
	if err := store.CompleteRunning(ctx, jobID); err != nil {
		t.Fatalf("CompleteRunning: %v", err)
	}

	got, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "completed" {
		t.Errorf("State after every device reported = %q, want %q", got.State, "completed")
	}

	byDevice := map[string]dispatch.JobTask{}
	for _, task := range tasks {
		byDevice[task.DeviceID] = task
	}
	if r := byDevice["dev-1"].Result; r != dispatch.ResultSucceeded {
		t.Errorf("dev-1 Result = %q, want %q", r, dispatch.ResultSucceeded)
	}
	if r := byDevice["dev-2"].Result; r != dispatch.ResultFailed {
		t.Errorf("dev-2 Result = %q, want %q", r, dispatch.ResultFailed)
	}
	if reason := byDevice["dev-2"].ResultReason; reason != "the play did not converge" {
		t.Errorf("dev-2 ResultReason = %q, want the Runner's own sentence", reason)
	}
	// The dispatch outcome is untouched by the result. Both facts survive,
	// which is the whole reason they are separate fields: a device that
	// was dispatched and then failed must stay distinguishable from one
	// that was never dispatched at all.
	if o := byDevice["dev-2"].Outcome; o != dispatch.OutcomeDispatched {
		t.Errorf("dev-2 Outcome = %q, want %q unchanged by its result", o, dispatch.OutcomeDispatched)
	}
	if byDevice["dev-1"].FinishedAt.IsZero() {
		t.Error("dev-1 FinishedAt is the zero time, want the moment it reported")
	}
}

// TestEntJobStore_RecordResult_IsIdempotent is the property JetStream
// forces on anything reading off the mesh.
//
// Delivery is at-least-once, so the same result really does arrive twice.
// The count of outstanding devices is taken from the stored rows rather
// than from a counter for exactly this reason: a decrementing counter
// would run past zero on the duplicate and declare a two-device job
// finished while one device was still running.
func TestEntJobStore_RecordResult_IsIdempotent(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1", "dev-2")

	for i := range 3 {
		complete, err := store.RecordResult(ctx, jobID, "dev-1", dispatch.ResultSucceeded, "", 0)
		if err != nil {
			t.Fatalf("RecordResult(dev-1) delivery %d: %v", i+1, err)
		}
		if complete {
			t.Fatalf("delivery %d of dev-1's result reported the job complete while dev-2 was still outstanding", i+1)
		}
	}

	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "running" {
		t.Errorf("State after three deliveries of one device's result = %q, want %q", got.State, "running")
	}
}

// TestEntJobStore_CompleteRunning_OnlyEndsAJobOnce covers the race two
// results arriving together produce: both can believe they were last.
func TestEntJobStore_CompleteRunning_OnlyEndsAJobOnce(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1")

	if _, err := store.RecordResult(ctx, jobID, "dev-1", dispatch.ResultSucceeded, "", 0); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if err := store.CompleteRunning(ctx, jobID); err != nil {
		t.Fatalf("first CompleteRunning: %v", err)
	}
	// The second call is the duplicate, and it must be a no-op rather than
	// an error: nothing went wrong, another result simply got there first.
	if err := store.CompleteRunning(ctx, jobID); err != nil {
		t.Fatalf("second CompleteRunning returned %v, want nil: losing this race is ordinary, not an error", err)
	}

	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "completed" {
		t.Errorf("State = %q, want %q", got.State, "completed")
	}
}

// TestEntJobStore_CompleteRunning_WillNotReviveACanceledJob is the other
// half of the cancel interaction, and it is not hypothetical.
//
// Cancelling does not stop work already running on a device, so those
// devices finish and report back afterwards. Without the "running" guard,
// the last of those results would move a job an operator deliberately
// stopped into "completed", telling them the run they cancelled had
// finished normally.
func TestEntJobStore_CompleteRunning_WillNotReviveACanceledJob(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)
	jobID, _ := runningJob(t, store, "dev-1")

	if err := store.Cancel(ctx, jobID, "operator"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// The device was already executing when the cancel landed, so its
	// result arrives anyway. Recording it is right: it is what happened.
	complete, err := store.RecordResult(ctx, jobID, "dev-1", dispatch.ResultSucceeded, "", 0)
	if err != nil {
		t.Fatalf("RecordResult after the cancel: %v", err)
	}
	if !complete {
		t.Fatal("the last outstanding device did not report the job as having nothing left to wait for")
	}
	if err := store.CompleteRunning(ctx, jobID); err != nil {
		t.Fatalf("CompleteRunning on a canceled job returned %v, want nil", err)
	}

	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "canceled" {
		t.Errorf("State = %q, want %q: a late result must not turn a cancelled job into a completed one", got.State, "canceled")
	}
}

// TestEntJobStore_RecordResult_RefusesADeviceTheJobNeverDispatchedTo
// proves a result can only land on a device that actually ran.
//
// A skipped device never executed, so a result naming one is a message
// about something that did not happen. Accepting it would also corrupt the
// count of outstanding devices, since a skipped task carrying a result
// would satisfy a condition it was never meant to be part of.
func TestEntJobStore_RecordResult_RefusesADeviceTheJobNeverDispatchedTo(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
		DeviceID: "dev-skipped", DeviceName: "router-skipped",
		Outcome: dispatch.OutcomeSkipped, Reason: "quarantined",
	}); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}

	for _, deviceID := range []string{"dev-skipped", "dev-never-seen"} {
		t.Run(deviceID, func(t *testing.T) {
			_, err := store.RecordResult(ctx, job.JobID, deviceID, dispatch.ResultSucceeded, "", 0)
			if !errors.Is(err, dispatch.ErrJobNotFound) {
				t.Fatalf("RecordResult for %q = %v, want a wrapped ErrJobNotFound", deviceID, err)
			}
		})
	}
}

// TestParseResult refuses what a Runner must never put on the wire.
//
// The empty string is the case worth pinning: it is the ordinary stored
// value for a device that has not reported, so the obvious implementation
// accepts it. Off the mesh it means a message naming no outcome, and
// treating that as "not reported yet" would leave a job running forever
// while looking handled.
func TestParseResult(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    dispatch.Result
		wantErr bool
	}{
		{in: "succeeded", want: dispatch.ResultSucceeded},
		{in: "failed", want: dispatch.ResultFailed},
		{in: "", wantErr: true},
		{in: "completed", wantErr: true},
		{in: "SUCCEEDED", wantErr: true},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := dispatch.ParseResult(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseResult(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseResult(%q) returned unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseResult(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestEntJobStore_SettleRunning_CompletesAJobEveryDeviceAlreadyReported is
// the lost wakeup, and it is a permanent hang rather than a delay.
//
// The task rows are written INSIDE the fan-out loop and SettleRunning runs
// after it, so a result can be recorded while the loop is still going. If
// the last outstanding result lands in that window, RecordResult correctly
// says the job is waiting on nothing, and the CompleteRunning that follows
// matches no row because the job is still "fanning_out" -- which is one of
// the ordinary no-op endings, not an error. Without the re-check in
// SettleRunning the job is then parked in "running" with zero outstanding
// tasks and nothing in the system that would ever end it, and nothing
// sweeps "running".
//
// The window is not narrow. The e2e suite's runbook is a noop that finishes
// in microseconds, which is exactly the shape that reports back before a
// fan-out over the rest of a group has finished walking.
func TestEntJobStore_SettleRunning_CompletesAJobEveryDeviceAlreadyReported(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "launcher"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
		DeviceID: "dev-1", DeviceName: "dev-1", Outcome: dispatch.OutcomeDispatched,
	}); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}

	// The result beats the end of the fan-out. The job is still
	// "fanning_out" here, which is the whole point.
	complete, err := store.RecordResult(ctx, job.JobID, "dev-1", dispatch.ResultSucceeded, "", 0)
	if err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if !complete {
		t.Fatal("RecordResult said the job was still waiting on something, with its only device reported")
	}
	// What the consumer does next, and what does nothing because the job
	// has not reached "running" yet.
	if err := store.CompleteRunning(ctx, job.JobID); err != nil {
		t.Fatalf("CompleteRunning: %v", err)
	}
	if got, _, getErr := store.Get(ctx, job.JobID); getErr != nil {
		t.Fatalf("Get: %v", getErr)
	} else if got.State != "fanning_out" {
		t.Fatalf("State = %q before settling, want %q: this test no longer exercises the window it exists for",
			got.State, "fanning_out")
	}

	// Now the fan-out finishes.
	if err := store.SettleRunning(ctx, job.JobID, fence, 1, 0, 0); err != nil {
		t.Fatalf("SettleRunning: %v", err)
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "completed" {
		t.Errorf("State = %q, want %q: the job is parked in a state nothing sweeps, with nothing left to end it",
			got.State, "completed")
	}
}

// TestEntJobStore_SettleRunning_LeavesAJobStillWaitingInRunning is the
// negative control for the test above.
//
// Without it, SettleRunning could complete every job unconditionally and
// the lost-wakeup test would still pass -- which would end a run the moment
// its fan-out finished and throw away the whole point of the "running"
// state.
func TestEntJobStore_SettleRunning_LeavesAJobStillWaitingInRunning(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	// Two devices, one reported. The job is still waiting on the other.
	jobID, _ := runningJob(t, store, "dev-1", "dev-2")
	if _, err := store.RecordResult(ctx, jobID, "dev-1", dispatch.ResultSucceeded, "", 0); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}

	got, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "running" {
		t.Errorf("State = %q, want %q: a job with a device still executing was ended early", got.State, "running")
	}
}
