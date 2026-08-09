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
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus)

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

	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatalf("Worker.HandleJobRequested (reached via the reaper's republish) returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the reaper's republish to reach the subscribed worker")
	}
	cancel()

	gotJob, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if gotJob.State != "completed" {
		t.Fatalf("job State after reaper-triggered reclaim = %q, want %q (not left in fanning_out)", gotJob.State, "completed")
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
