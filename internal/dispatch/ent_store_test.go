package dispatch_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	// Aliased entjob: this file's existing tests already name their local
	// *dispatch.Job variable "job", so importing internal/ent/job under its
	// default name would shadow every one of those existing local
	// variables rather than the other way around.
	entjob "github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	_ "github.com/mattn/go-sqlite3"
)

// newTestStore spins up a real in-memory SQLite database and returns a
// JobStore over it. Per AGENTS.md's RULE 0, these tests exercise the
// actual ent write path, the exact conditional-update idiom production
// code runs, never a mock ent client standing in for it.
//
// Each test gets its own database name so shared-cache SQLite does not
// leak rows between tests, mirroring internal/inventory/ent_save_test.go's
// own newTestRepo.
func newTestStore(t testing.TB) (dispatch.JobStore, *ent.Client) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	return dispatch.NewEntJobStore(client), client
}

func TestEntJobStore_CreateAndGet(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{
		RunbookID: "pb-1",
		GroupName: "routers",
		Actor:     "user@example.com",
	}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	if job.JobID == "" {
		t.Fatal("Create did not populate a generated JobID")
	}
	if job.State != "pending" {
		t.Errorf("newly created job State = %q, want %q", job.State, "pending")
	}

	got, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.RunbookID != "pb-1" || got.GroupName != "routers" || got.Actor != "user@example.com" {
		t.Errorf("Get returned %+v, want RunbookID=pb-1 GroupName=routers Actor=user@example.com", got)
	}
	if got.State != "pending" {
		t.Errorf("Get State = %q, want %q", got.State, "pending")
	}
	if len(tasks) != 0 {
		t.Errorf("Get returned %d tasks for a fresh job, want 0", len(tasks))
	}
}

// TestEntJobStore_CreateAndGet_CapturesLaunchFields proves the job record
// carries what a launch was configured with: the first hop of
// AWX_PARITY_ROADMAP.md's launch-fields-reach-execution phase, ahead of the
// wire and either adapter actually consuming it.
func TestEntJobStore_CreateAndGet_CapturesLaunchFields(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{
		RunbookID: "pb-1",
		Actor:     "user@example.com",
		Fields:    launch.Fields{"limit": "edge-*", "forks": 5},
		ExtraVars: map[string]any{"target_version": "17.3"},
	}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.Fields.String("limit") != "edge-*" || got.Fields.Int("forks") != 5 {
		t.Errorf("Get returned Fields %+v, want limit=edge-* forks=5", got.Fields)
	}
	if got.ExtraVars["target_version"] != "17.3" {
		t.Errorf("Get returned ExtraVars %+v, want target_version=17.3", got.ExtraVars)
	}
}

// TestEntJobStore_CreateAndGet_LeavesEmptyLaunchFieldsAbsent proves an
// ordinary launch that opened nothing is not indistinguishable, on
// inspection, from one this store simply forgot to persist: Fields and
// ExtraVars come back nil rather than an empty, present map.
func TestEntJobStore_CreateAndGet_LeavesEmptyLaunchFieldsAbsent(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", Actor: "user@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.Fields != nil {
		t.Errorf("Get returned Fields %+v for a launch that opened nothing, want nil", got.Fields)
	}
	if got.ExtraVars != nil {
		t.Errorf("Get returned ExtraVars %+v for a launch that opened nothing, want nil", got.ExtraVars)
	}
}

func TestEntJobStore_Get_NotFound(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	_, _, err := store.Get(ctx, "does-not-exist")
	if !errors.Is(err, dispatch.ErrJobNotFound) {
		t.Fatalf("Get on an unknown job = %v, want a wrapped ErrJobNotFound", err)
	}
}

// TestEntJobStore_BeginFanOut_IdempotencyGuard is the direct proof of this
// package's own central correctness claim: a redelivered job.requested
// message calling BeginFanOut a second time must not be told it may
// reprocess, and the job's own stored state must be unchanged by that
// second call, still "fanning_out", not bounced back to "pending" or
// silently advanced again.
func TestEntJobStore_BeginFanOut_IdempotencyGuard(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	began, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("first BeginFanOut returned unexpected error: %v", err)
	}
	if !began {
		t.Fatal("first BeginFanOut on a pending job returned began=false, want true")
	}
	if fence == 0 {
		t.Error("first BeginFanOut returned fence=0, want a nonzero fencing token")
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.State != "fanning_out" {
		t.Fatalf("job State after first BeginFanOut = %q, want %q", got.State, "fanning_out")
	}

	began, _, err = store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("second BeginFanOut returned unexpected error: %v", err)
	}
	if began {
		t.Fatal("second BeginFanOut on an already-fanning_out job returned began=true, want false")
	}

	got, _, err = store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.State != "fanning_out" {
		t.Errorf("job State after second BeginFanOut = %q, want unchanged %q", got.State, "fanning_out")
	}
}

func TestEntJobStore_BeginFanOut_NotFound(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	_, _, err := store.BeginFanOut(ctx, "does-not-exist", time.Hour)
	if !errors.Is(err, dispatch.ErrJobNotFound) {
		t.Fatalf("BeginFanOut on an unknown job = %v, want a wrapped ErrJobNotFound", err)
	}
}

// TestEntJobStore_BeginFanOut_DoesNotReclaimWithinLease proves the other
// half of the staleAfter guard: a "fanning_out" job whose heartbeat is
// still within the lease must NOT be reclaimed, since another delivery may
// genuinely still be actively working on it. Reclaiming too eagerly would
// reintroduce the exact double-fan-out the idempotency guard exists to
// prevent.
func TestEntJobStore_BeginFanOut_DoesNotReclaimWithinLease(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	began, _, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil || !began {
		t.Fatalf("first BeginFanOut = (%v, %v), want (true, nil)", began, err)
	}

	// Immediately redelivered, well within the one-hour lease: this must
	// still be refused, exactly like TestEntJobStore_BeginFanOut_IdempotencyGuard.
	began, _, err = store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("second BeginFanOut returned unexpected error: %v", err)
	}
	if began {
		t.Fatal("second BeginFanOut within the lease returned began=true, want false")
	}
}

// TestEntJobStore_BeginFanOut_ReclaimsStaleFanningOut is the direct proof
// of the fix for the finding that a Worker crashing between BeginFanOut
// and Complete/Fail stranded a job in "fanning_out" forever: once a
// claimed job's heartbeat has gone quiet for longer than staleAfter, a
// later BeginFanOut call must be allowed to reclaim it rather than
// refusing forever.
func TestEntJobStore_BeginFanOut_ReclaimsStaleFanningOut(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	began, firstFence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil || !began {
		t.Fatalf("first BeginFanOut = (%v, %v), want (true, nil)", began, err)
	}

	// Simulate the crashed Worker never coming back: backdate the row's
	// own updated_at, standing in for real wall-clock time passing with no
	// RecordTask heartbeat ever refreshing it. Direct client access, not
	// through JobStore, since no JobStore method exists to set a
	// timestamp: this is test setup for a scenario, not the behavior under
	// test.
	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(job.JobID)).
		SetUpdatedAt(time.Now().Add(-2 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate job updated_at: %v", err)
	}

	began, reclaimFence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("reclaim BeginFanOut returned unexpected error: %v", err)
	}
	if !began {
		t.Fatal("BeginFanOut on a stale fanning_out job returned began=false, want true (reclaim)")
	}
	// The fencing token's whole purpose (see internal/ent/schema/job.go's
	// fence field comment): a reclaim must obtain a strictly higher fence
	// than the claim it is superseding, so the superseded caller's own,
	// still-held fence value can never again satisfy a FenceEQ guard.
	if reclaimFence <= firstFence {
		t.Fatalf("reclaim fence = %d, want strictly greater than the original claim's fence %d", reclaimFence, firstFence)
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.State != "fanning_out" {
		t.Fatalf("job State after reclaim = %q, want unchanged %q", got.State, "fanning_out")
	}
}

// TestEntJobStore_ListStaleFanOuts proves the read-only scan Reaper
// (reaper.go) depends on returns exactly the jobs BeginFanOut's own
// reclaim branch would agree are eligible, and nothing else: a fresh
// fanning_out job (still within its lease) is excluded, and a pending job
// that never began fan-out at all is excluded too, even though both share
// nothing in common with a stale fanning_out row except existing.
func TestEntJobStore_ListStaleFanOuts(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)

	// never-started: still "pending", never claimed.
	neverStarted := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, neverStarted); err != nil {
		t.Fatalf("Create(neverStarted) returned unexpected error: %v", err)
	}

	// fresh: claimed moments ago, well within any realistic staleAfter.
	fresh := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, fresh); err != nil {
		t.Fatalf("Create(fresh) returned unexpected error: %v", err)
	}
	if began, _, err := store.BeginFanOut(ctx, fresh.JobID, time.Hour); err != nil || !began {
		t.Fatalf("BeginFanOut(fresh) = (%v, %v), want (true, nil)", began, err)
	}

	// stale: claimed, then its heartbeat backdated well past staleAfter,
	// standing in for a Worker that crashed mid-fan-out and never came
	// back.
	stale := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, stale); err != nil {
		t.Fatalf("Create(stale) returned unexpected error: %v", err)
	}
	if began, _, err := store.BeginFanOut(ctx, stale.JobID, time.Hour); err != nil || !began {
		t.Fatalf("BeginFanOut(stale) = (%v, %v), want (true, nil)", began, err)
	}
	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(stale.JobID)).
		SetUpdatedAt(time.Now().Add(-2 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate stale job's updated_at: %v", err)
	}

	// completed: a fan-out that finished normally, not eligible no matter
	// how old.
	completed := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, completed); err != nil {
		t.Fatalf("Create(completed) returned unexpected error: %v", err)
	}
	began, fence, err := store.BeginFanOut(ctx, completed.JobID, time.Hour)
	if err != nil || !began {
		t.Fatalf("BeginFanOut(completed) = (%v, %v), want (true, nil)", began, err)
	}
	if err := store.Complete(ctx, completed.JobID, fence, 1, 0, 0); err != nil {
		t.Fatalf("Complete(completed) returned unexpected error: %v", err)
	}
	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(completed.JobID)).
		SetUpdatedAt(time.Now().Add(-2 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate completed job's updated_at: %v", err)
	}

	got, err := store.ListStaleFanOuts(ctx, time.Hour)
	if err != nil {
		t.Fatalf("ListStaleFanOuts returned unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != stale.JobID {
		t.Fatalf("ListStaleFanOuts = %v, want exactly [%q]", got, stale.JobID)
	}
}

// TestEntJobStore_StaleReclaimFencesOutOriginalCaller is the direct proof
// of the fencing-token fix (Finding 2): once a stale reclaim bumps a job's
// fence, the ORIGINAL caller's RecordTask, Complete, and Fail calls, still
// presenting the fence value its own (now-superseded) BeginFanOut call
// obtained, must each be rejected with a wrapped ErrFenced rather than
// allowed to keep mutating a job a second worker now owns. The RECLAIMING
// caller's own writes, presenting the new fence, must succeed normally:
// fencing rejects the superseded party, never the legitimate new owner.
func TestEntJobStore_StaleReclaimFencesOutOriginalCaller(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	// The "original" caller: claims the fan-out first, exactly like a
	// Worker that then hangs on a slow downstream call for longer than
	// staleAfter (Finding 3's own scenario) without ever crashing outright.
	began, originalFence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil || !began {
		t.Fatalf("original BeginFanOut = (%v, %v), want (true, nil)", began, err)
	}

	// Backdate updated_at so a second delivery's BeginFanOut sees this
	// claim as stale and reclaims it, exactly as
	// TestEntJobStore_BeginFanOut_ReclaimsStaleFanningOut already proves,
	// while the "original" caller above is still, unbeknownst to itself,
	// about to keep making calls with its now-superseded fence.
	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(job.JobID)).
		SetUpdatedAt(time.Now().Add(-2 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate job updated_at: %v", err)
	}

	// The "reclaiming" caller: a second delivery's BeginFanOut, superseding
	// the original claim and obtaining a strictly higher fence.
	began, reclaimFence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil || !began {
		t.Fatalf("reclaim BeginFanOut = (%v, %v), want (true, nil)", began, err)
	}
	if reclaimFence == originalFence {
		t.Fatalf("reclaim fence %d equals original fence %d, want distinct", reclaimFence, originalFence)
	}

	// The original, now-superseded caller's RecordTask, still presenting
	// originalFence, must be rejected.
	err = store.RecordTask(ctx, job.JobID, originalFence, dispatch.JobTask{
		DeviceID:   "dev-1",
		DeviceName: "router-1",
		Outcome:    dispatch.OutcomeDispatched,
	})
	if !errors.Is(err, dispatch.ErrFenced) {
		t.Errorf("original caller's RecordTask = %v, want a wrapped ErrFenced", err)
	}

	// The original, now-superseded caller's Complete, still presenting
	// originalFence, must also be rejected.
	err = store.Complete(ctx, job.JobID, originalFence, 1, 0, 0)
	if !errors.Is(err, dispatch.ErrFenced) {
		t.Errorf("original caller's Complete = %v, want a wrapped ErrFenced", err)
	}

	// The original, now-superseded caller's Fail, still presenting
	// originalFence, must also be rejected.
	err = store.Fail(ctx, job.JobID, originalFence, "original caller failed after being superseded")
	if !errors.Is(err, dispatch.ErrFenced) {
		t.Errorf("original caller's Fail = %v, want a wrapped ErrFenced", err)
	}

	// The reclaiming caller's own RecordTask, presenting the current
	// reclaimFence, must succeed normally: fencing rejects only the
	// superseded party, never the legitimate current owner.
	if err := store.RecordTask(ctx, job.JobID, reclaimFence, dispatch.JobTask{
		DeviceID:   "dev-1",
		DeviceName: "router-1",
		Outcome:    dispatch.OutcomeDispatched,
	}); err != nil {
		t.Fatalf("reclaiming caller's RecordTask returned unexpected error: %v", err)
	}

	// The reclaiming caller's own Complete, presenting reclaimFence, must
	// also succeed normally and actually finish the job.
	if err := store.Complete(ctx, job.JobID, reclaimFence, 1, 0, 0); err != nil {
		t.Fatalf("reclaiming caller's Complete returned unexpected error: %v", err)
	}

	got, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.State != "completed" {
		t.Fatalf("job State = %q, want %q (the reclaiming caller's own Complete)", got.State, "completed")
	}
	if got.DispatchedCount != 1 {
		t.Fatalf("DispatchedCount = %d, want 1", got.DispatchedCount)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v, want exactly 1 (the original caller's rejected RecordTask wrote nothing)", tasks)
	}
}

func TestEntJobStore_RecordTask(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	task := dispatch.JobTask{
		DeviceID:   "dev-1",
		DeviceName: "router-1",
		Outcome:    dispatch.OutcomeSkipped,
		Reason:     `device "router-1" is quarantined, not active`,
	}
	// fence 0: a freshly created job's stored fence defaults to 0 (it has
	// never been claimed by a BeginFanOut call), so this is the fence
	// value RecordTask's own FenceEQ guard expects here.
	if err := store.RecordTask(ctx, job.JobID, 0, task); err != nil {
		t.Fatalf("RecordTask returned unexpected error: %v", err)
	}

	_, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Get returned %d tasks, want 1", len(tasks))
	}
	if tasks[0] != task {
		t.Errorf("recorded task = %+v, want %+v", tasks[0], task)
	}
}

// TestEntJobStore_RecordTask_NoBuiltInDedup proves RecordTask, by design
// (see its own doc comment), performs no idempotency check of its own:
// calling it twice for the same (job, device) pair inserts two task rows.
// The corresponding guard against that ever happening for a real reclaimed
// job lives in Worker.HandleJobRequested instead (see
// TestWorker_HandleJobRequested_StaleFanOutIsReclaimed), using the task
// list Get already returns; this test exists so a future change that
// quietly pushes the guard back down into this method does not silently
// change RecordTask's documented contract without an update here too.
func TestEntJobStore_RecordTask_NoBuiltInDedup(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	task := dispatch.JobTask{DeviceID: "dev-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched}
	if err := store.RecordTask(ctx, job.JobID, 0, task); err != nil {
		t.Fatalf("first RecordTask returned unexpected error: %v", err)
	}
	if err := store.RecordTask(ctx, job.JobID, 0, task); err != nil {
		t.Fatalf("second RecordTask for the same device returned unexpected error: %v", err)
	}

	_, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks after recording the same device twice = %d, want 2 (RecordTask performs no dedup of its own)", len(tasks))
	}
}

// TestEntJobStore_RecordTask_RefreshesHeartbeat proves RecordTask's other
// job: pushing the job row's own updated_at forward, which is what lets
// BeginFanOut's staleAfter reclaim tell an actively-progressing fan-out
// apart from an abandoned one.
func TestEntJobStore_RecordTask_RefreshesHeartbeat(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(job.JobID)).
		SetUpdatedAt(time.Now().Add(-time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate job updated_at: %v", err)
	}

	task := dispatch.JobTask{DeviceID: "dev-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched}
	if err := store.RecordTask(ctx, job.JobID, 0, task); err != nil {
		t.Fatalf("RecordTask returned unexpected error: %v", err)
	}

	row, err := client.Job.Query().Where(entjob.JobIDEQ(job.JobID)).Only(ctx)
	if err != nil {
		t.Fatalf("failed to reload job: %v", err)
	}
	if time.Since(row.UpdatedAt) > time.Minute {
		t.Errorf("job updated_at after RecordTask = %v, want refreshed to roughly now", row.UpdatedAt)
	}
}

// TestEntJobStore_Complete claims the fan-out via BeginFanOut first, the
// same real path a Worker follows, rather than calling Complete directly
// on a still-"pending" job: Complete's own conditional update now requires
// state = fanning_out (see Complete's own doc comment), so this is the
// only way to reach it under the fixed contract, and it exercises the
// production BeginFanOut-then-Complete sequence end to end.
func TestEntJobStore_Complete(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut returned unexpected error: %v", err)
	}

	if err := store.Complete(ctx, job.JobID, fence, 3, 2, 1); err != nil {
		t.Fatalf("Complete returned unexpected error: %v", err)
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.State != "completed" {
		t.Errorf("State after Complete = %q, want %q", got.State, "completed")
	}
	if got.DispatchedCount != 3 || got.SkippedCount != 2 || got.FailedCount != 1 {
		t.Errorf("tallies after Complete = dispatched=%d skipped=%d failed=%d, want 3/2/1",
			got.DispatchedCount, got.SkippedCount, got.FailedCount)
	}
}

// TestEntJobStore_Complete_SecondCallRejected is the direct proof of the
// fix for the finding that a second Complete call for an already-terminal
// job used to silently overwrite the first call's own State and tallies
// with no error and no detection: Complete's own state-plus-fence guard
// (see its doc comment) must now reject the second call outright.
func TestEntJobStore_Complete_SecondCallRejected(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut returned unexpected error: %v", err)
	}
	if err := store.Complete(ctx, job.JobID, fence, 3, 2, 1); err != nil {
		t.Fatalf("first Complete returned unexpected error: %v", err)
	}

	// A second Complete call presenting the identical fence a caller
	// legitimately obtained (e.g. a duplicate terminal write from a retry
	// or a redelivery that reached this far), tallies that would silently
	// overwrite the first call's own numbers if this guard were missing.
	err = store.Complete(ctx, job.JobID, fence, 99, 99, 99)
	if !errors.Is(err, dispatch.ErrFenced) {
		t.Fatalf("second Complete call = %v, want a wrapped ErrFenced (job is no longer fanning_out)", err)
	}

	got, _, getErr := store.Get(ctx, job.JobID)
	if getErr != nil {
		t.Fatalf("Get returned unexpected error: %v", getErr)
	}
	if got.DispatchedCount != 3 || got.SkippedCount != 2 || got.FailedCount != 1 {
		t.Errorf("tallies after the rejected second Complete = dispatched=%d skipped=%d failed=%d, want the first call's own 3/2/1 unchanged",
			got.DispatchedCount, got.SkippedCount, got.FailedCount)
	}
}

// TestEntJobStore_Fail claims the fan-out via BeginFanOut first, the same
// reasoning TestEntJobStore_Complete's own doc comment gives for Complete.
func TestEntJobStore_Fail(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "no-such-runbook", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut returned unexpected error: %v", err)
	}

	reason := `runbook "no-such-runbook" not found`
	if err := store.Fail(ctx, job.JobID, fence, reason); err != nil {
		t.Fatalf("Fail returned unexpected error: %v", err)
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("State after Fail = %q, want %q", got.State, "failed")
	}
}

// TestEntJobStore_Fail_SecondCallRejected mirrors
// TestEntJobStore_Complete_SecondCallRejected for Fail: a second Fail call
// for an already-terminal job (this time itself already "failed", rather
// than "completed") must be rejected, not silently overwrite
// failure_reason a second time.
func TestEntJobStore_Fail_SecondCallRejected(t *testing.T) {
	ctx := t.Context()
	store, _ := newTestStore(t)

	job := &dispatch.Job{RunbookID: "no-such-runbook", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut returned unexpected error: %v", err)
	}
	firstReason := `runbook "no-such-runbook" not found`
	if err := store.Fail(ctx, job.JobID, fence, firstReason); err != nil {
		t.Fatalf("first Fail returned unexpected error: %v", err)
	}

	err = store.Fail(ctx, job.JobID, fence, "a different, later reason")
	if !errors.Is(err, dispatch.ErrFenced) {
		t.Fatalf("second Fail call = %v, want a wrapped ErrFenced (job is no longer fanning_out)", err)
	}

	got, _, getErr := store.Get(ctx, job.JobID)
	if getErr != nil {
		t.Fatalf("Get returned unexpected error: %v", getErr)
	}
	if got.State != "failed" {
		t.Errorf("State after the rejected second Fail = %q, want unchanged %q", got.State, "failed")
	}
}
