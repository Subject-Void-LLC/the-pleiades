// The forks window's bound, proved on both databases the Controller runs
// on. A windowed job holds at most forks devices running at once because
// each holds a numbered slot and the unique index on (job, slot) refuses a
// second claim on a taken slot (internal/ent/schema/job_task.go). That is
// a property of the migrations each dialect applies, not of Go code, so it
// is proved here, through ent.OpenDatabase's real versioned migrations,
// with two clients standing in for two Controller replicas.
package ent_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

func TestWindowSlotsBoundClaimsAcrossReplicas(t *testing.T) {
	const devices, forks = 12, 3
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			dsn := backend.newDSN(t)
			replicas := make([]dispatch.JobStore, 2)
			for i := range replicas {
				client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
				if err != nil {
					t.Fatalf("OpenDatabase(replica %d): %v", i, err)
				}
				t.Cleanup(func() { _ = client.Close() })
				replicas[i] = dispatch.NewEntJobStore(client)
			}
			store := replicas[0]

			// A running job with every device queued: what a windowed
			// fan-out leaves behind.
			job := &dispatch.Job{RunbookID: "pb", GroupName: "g", Actor: "test"}
			if err := store.Create(ctx, job); err != nil {
				t.Fatalf("Create: %v", err)
			}
			began, fence, err := store.BeginFanOut(ctx, job.JobID, 0)
			if err != nil || !began {
				t.Fatalf("BeginFanOut = %v, %v", began, err)
			}
			for i := 0; i < devices; i++ {
				task := dispatch.JobTask{DeviceID: fmt.Sprintf("dev-%d", i), DeviceName: fmt.Sprintf("d%d", i), Outcome: dispatch.OutcomeQueued}
				if err := store.RecordTask(ctx, job.JobID, fence, task); err != nil {
					t.Fatalf("RecordTask: %v", err)
				}
			}
			if err := store.SettleRunning(ctx, job.JobID, fence, 0, 0, 0); err != nil {
				t.Fatalf("SettleRunning: %v", err)
			}

			// Every replica tries every device in every slot at once: far
			// more attempts than places.
			var made atomic.Int64
			var wg sync.WaitGroup
			errs := make(chan error, 2*devices*forks)
			for r := range replicas {
				for d := 0; d < devices; d++ {
					for s := 0; s < forks; s++ {
						wg.Add(1)
						go func(st dispatch.JobStore, device string, slot int) {
							defer wg.Done()
							res, err := st.ClaimQueued(ctx, job.JobID, device, slot)
							if err != nil {
								errs <- err
								return
							}
							if res == dispatch.ClaimMade {
								made.Add(1)
							}
						}(replicas[r], fmt.Sprintf("dev-%d", d), s)
					}
				}
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				// A lock wait timing out under this much contention would
				// surface here; it is reported, not ignored, because it
				// would mean the bound holds only by luck of scheduling.
				t.Errorf("ClaimQueued: %v", err)
			}

			if got := made.Load(); got != forks {
				t.Fatalf("%d claims succeeded across both replicas, want exactly forks (%d)", got, forks)
			}
			held, err := store.HeldSlots(ctx, job.JobID)
			if err != nil {
				t.Fatalf("HeldSlots: %v", err)
			}
			seen := map[int]bool{}
			for _, s := range held {
				if s < 0 || s >= forks || seen[s] {
					t.Errorf("held slots %v, want each of 0..%d once", held, forks-1)
				}
				seen[s] = true
			}
			got, tasks, err := store.Get(ctx, job.JobID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			queued := 0
			for _, task := range tasks {
				if task.Outcome == dispatch.OutcomeQueued {
					queued++
				}
			}
			if queued != devices-forks || got.DispatchedCount != forks {
				t.Errorf("%d queued and %d counted dispatched, want %d and %d", queued, got.DispatchedCount, devices-forks, forks)
			}

			// A result frees its slot, and exactly one more claim fits.
			var reporter string
			for _, task := range tasks {
				if task.Outcome == dispatch.OutcomeDispatched {
					reporter = task.DeviceID
					break
				}
			}
			if _, err := store.RecordResult(ctx, job.JobID, reporter, dispatch.ResultSucceeded, "", 0); err != nil {
				t.Fatalf("RecordResult: %v", err)
			}
			held, err = store.HeldSlots(ctx, job.JobID)
			if err != nil || len(held) != forks-1 {
				t.Fatalf("after a result, held slots %v (%v), want %d", held, err, forks-1)
			}
			made.Store(0)
			for d := 0; d < devices; d++ {
				for s := 0; s < forks; s++ {
					if res, err := replicas[d%2].ClaimQueued(ctx, job.JobID, fmt.Sprintf("dev-%d", d), s); err == nil && res == dispatch.ClaimMade {
						made.Add(1)
					}
				}
			}
			if got := made.Load(); got != 1 {
				t.Errorf("after one result freed one slot, %d more claims succeeded, want 1", got)
			}
		})
	}
}

// TestJobTaskSlotIndexRefusesASecondClaim proves the unique index the
// window's migrations add, directly: one row per slot per job, any number
// of rows with no slot at all, and a new row's waiting flag defaulting to
// false, which is what every row written before the window reads as.
func TestJobTaskSlotIndexRefusesASecondClaim(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			client, err := ent.OpenDatabase(ctx, ent.Config{DSN: backend.newDSN(t)})
			if err != nil {
				t.Fatalf("OpenDatabase: %v", err)
			}
			defer client.Close()

			job, err := client.Job.Create().SetJobID("0199aaaa-0000-7000-8000-000000000001").SetRunbookID("pb").SetGroupName("g").SetActor("test").Save(ctx)
			if err != nil {
				t.Fatalf("create job: %v", err)
			}
			task := func(device string, slot *int) error {
				c := client.JobTask.Create().SetJobID(job.ID).SetDeviceID(device).SetDeviceName(device).SetOutcome("dispatched")
				if slot != nil {
					c = c.SetSlot(*slot)
				}
				_, err := c.Save(ctx)
				return err
			}
			zero := 0
			if err := task("a", &zero); err != nil {
				t.Fatalf("first row: %v", err)
			}
			if err := task("b", &zero); !ent.IsConstraintError(err) {
				t.Errorf("a second row in slot 0 = %v, want a constraint error", err)
			}
			for _, d := range []string{"c", "d", "e"} {
				if err := task(d, nil); err != nil {
					t.Errorf("row %s with no slot: %v, want any number allowed", d, err)
				}
			}
			rows, err := client.JobTask.Query().All(ctx)
			if err != nil {
				t.Fatalf("read rows: %v", err)
			}
			for _, r := range rows {
				if r.Waiting {
					t.Errorf("row %s was written without a waiting flag and reads waiting", r.DeviceID)
				}
			}
		})
	}
}
