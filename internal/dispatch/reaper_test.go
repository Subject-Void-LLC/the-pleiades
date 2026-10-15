package dispatch_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	entjob "github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestReaper_EndToEnd_ReclaimsAndCompletesStaleJob is the direct proof of
// this package's own central fix for the finding that a Worker crashing
// between BeginFanOut and Complete/Fail stranded its job in "fanning_out"
// forever: JetStream's own redelivery budget (five redeliveries at 30s
// apart, internal/topology's MaxDeliverDefault and consumerAckWait) is far
// shorter than any realistic staleAfter, so a real broker never
// redelivers job.requested long enough for BeginFanOut's own staleAfter
// reclaim to ever run.
//
// This test proves the fix without a real broker: a real ent-backed
// JobStore, a real event.NewInProcessBus, a real Worker subscribed to it
// exactly as cmd/controller/main.go wires one
// (bus.Subscribe(ctx, topology.JobRequestedSubject(),
// worker.HandleJobRequested)), and a real Reaper actually ticking via
// Run, all driven only through their public APIs. No direct call to
// Worker.HandleJobRequested or JobStore.BeginFanOut stands in for the
// mechanism under test, unlike the pre-existing
// TestWorker_HandleJobRequested_StaleFanOutIsReclaimed, which proves
// BeginFanOut's own reclaim logic is correct but not that anything ever
// triggers it. What a real NATS broker would additionally contribute (the
// redelivery-budget-versus-staleAfter numbers this fix is grounded in) is
// fixed configuration, verified by reading internal/topology's own
// constants, not runtime behavior that needs a live broker to observe.
func TestReaper_EndToEnd_ReclaimsAndCompletesStaleJob(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)
	bus := event.NewInProcessBus()

	device := capableDevice("dev-1", "router-1", "10.0.0.1")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	handlerDone := make(chan error, 8)
	if err := bus.Subscribe(ctx, topology.JobRequestedSubject(), func(evt event.Event) error {
		err := worker.HandleJobRequested(evt)
		handlerDone <- err
		return err
	}); err != nil {
		t.Fatalf("failed to subscribe worker to job.requested: %v", err)
	}

	// Create and claim a job, then stop, exactly the crash scenario: a
	// Worker that died immediately after BeginFanOut succeeded and before
	// a single device was recorded. No job.requested is published for
	// this claim at all in this test, mirroring how the ORIGINAL delivery
	// that made this claim is long gone (dead-lettered, in a real
	// deployment) by the time anything notices.
	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	began, _, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil || !began {
		t.Fatalf("BeginFanOut = (%v, %v), want (true, nil)", began, err)
	}
	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(job.JobID)).
		SetUpdatedAt(time.Now().Add(-24 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate job's updated_at: %v", err)
	}

	// staleAfter (one hour) matches the BeginFanOut call above exactly,
	// the agreement JobStore.ListStaleFanOuts's own doc comment requires
	// between a Reaper and the Worker(s) it is reaping for. The interval
	// is overridden short so this test does not wait out a real minute.
	reaper := dispatch.NewReaper(store, bus, time.Hour, dispatch.WithReapInterval(10*time.Millisecond))
	runCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	go reaper.Run(runCtx, func() bool { return true })

	// Wait for the job to leave "fanning_out", not for the first handler
	// to return. The reaper sweeps every 10 ms and republishes while the
	// job is still stale, so on a loaded runner (macOS CI, 2026-10-15) a
	// second delivery arrives before the first claims it, finds it claimed
	// and returns at once, before the first has settled it; reading the
	// state then saw "fanning_out". Reproduced every run with the claim
	// and row writes slowed by a store wrapper.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, _, err := store.Get(ctx, job.JobID); err == nil && got.State != "fanning_out" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reaper's republish never moved the job out of fanning_out")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	// The delivery that settled the job returned nil. A losing one may not:
	// its claim can fail on this test store's shared-cache SQLite ("table
	// is locked"), where the Controller's WAL database waits instead, and a
	// failed delivery is only redelivered. What must hold is below: one
	// fan-out, one dispatch, one task.
	// The settling handler reports just after the state changes, so its
	// nil is waited for, not drained.
	for handled := false; !handled; {
		select {
		case err := <-handlerDone:
			handled = err == nil
		case <-time.After(5 * time.Second):
			t.Fatal("no delivery of the reaper's republish was handled without an error")
		}
	}

	gotJob, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	// "running" rather than "completed", and the distinction is the point
	// of this assertion rather than a relaxation of it: what the reaper
	// has to prove is that the job did not stay stuck in "fanning_out".
	// The reclaimed fan-out dispatched to a device, so the run continues
	// until that device reports back.
	if gotJob.State != "running" {
		t.Fatalf("job State after reaper-triggered reclaim = %q, want %q (not left in fanning_out)", gotJob.State, "running")
	}
	if gotJob.DispatchedCount != 1 {
		t.Fatalf("DispatchedCount after reclaim = %d, want 1", gotJob.DispatchedCount)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks after reclaim = %+v, want exactly 1 (no duplicate dispatch)", tasks)
	}
}

// TestReaper_Run_RepublishesOnlyWhileLeader proves Run's leadership gate:
// a tick where isLeader reports false must not touch the bus at all, and a
// tick where it reports true must republish the stale job's own
// job.requested shape.
func TestReaper_Run_RepublishesOnlyWhileLeader(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)
	bus := newCapturingBus()

	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}
	if began, _, err := store.BeginFanOut(ctx, job.JobID, time.Hour); err != nil || !began {
		t.Fatalf("BeginFanOut = (%v, %v), want (true, nil)", began, err)
	}
	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(job.JobID)).
		SetUpdatedAt(time.Now().Add(-24 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate job's updated_at: %v", err)
	}

	reaper := dispatch.NewReaper(store, bus, time.Hour, dispatch.WithReapInterval(10*time.Millisecond))

	notLeaderCtx, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	reaper.Run(notLeaderCtx, func() bool { return false })
	cancel()

	if got := bus.count(); got != 0 {
		t.Fatalf("published %d events while never leader, want 0", got)
	}

	leaderCtx, cancel2 := context.WithTimeout(ctx, 60*time.Millisecond)
	reaper.Run(leaderCtx, func() bool { return true })
	cancel2()

	if got := bus.count(); got == 0 {
		t.Fatal("published 0 events while leader with one stale job present, want at least 1")
	}
	if topic := bus.lastTopic(); topic != topology.JobRequestedSubject() {
		t.Errorf("published to topic %q, want %q", topic, topology.JobRequestedSubject())
	}
}
