// Package dispatch_test holds the claim tests: the store follows facts.DecideClaim, and replicas racing for one claim produce one winner.
package dispatch_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// dispatchedDevice creates a job and one device the job dispatched to a
// Runner, the state a facts batch is admitted against, and returns the job's
// ID and the device's name.
func dispatchedDevice(t *testing.T, store dispatch.JobStore, client *ent.Client) (string, string) {
	t.Helper()
	ctx := t.Context()
	j := &dispatch.Job{RunbookID: "pb-1", Actor: "user@example.com"}
	if err := store.Create(ctx, j); err != nil {
		t.Fatal(err)
	}
	entJob, err := client.Job.Query().Where(job.JobIDEQ(j.JobID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.JobTask.Create().
		SetDeviceID("dev-1").SetDeviceName("web1").SetOutcome(jobtask.OutcomeDispatched).
		SetJob(entJob).Save(ctx); err != nil {
		t.Fatal(err)
	}
	return j.JobID, "dev-1"
}

// TestEntJobStore_ClaimBatch_FollowsTheRules: the store applies
// facts.DecideClaim to the row and writes what it decides, so a later batch
// is judged against the claim an earlier one wrote.
func TestEntJobStore_ClaimBatch_FollowsTheRules(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)
	jobID, device := dispatchedDevice(t, store, client)
	order := func(token uint64) facts.Order { return facts.Order{Source: "lease:e1", Token: token} }
	steps := []struct {
		name    string
		next    facts.Claim
		refused string
	}{
		{name: "the first batch sets the claim", next: facts.Claim{Attempt: 1, Runner: "r1", Order: order(5)}},
		{name: "the same Runner and order is accepted again", next: facts.Claim{Attempt: 1, Runner: "r1", Order: order(5)}},
		{name: "another Runner in the same attempt is refused", next: facts.Claim{Attempt: 1, Runner: "r2", Order: order(5)}, refused: "other than"},
		{name: "a later attempt on another Runner takes the device", next: facts.Claim{Attempt: 2, Runner: "r2", Order: order(9)}},
		{name: "the superseded attempt is refused after the takeover", next: facts.Claim{Attempt: 1, Runner: "r1", Order: order(5)}, refused: "from attempt 1"},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			refusal, err := store.ClaimBatch(ctx, jobID, device, s.next)
			if err != nil {
				t.Fatal(err)
			}
			if s.refused == "" && refusal != "" {
				t.Fatalf("refused with %q, want accepted", refusal)
			}
			if s.refused != "" && !strings.Contains(refusal, s.refused) {
				t.Fatalf("refusal %q, want it to mention %q", refusal, s.refused)
			}
		})
	}
	row, err := client.JobTask.Query().Where(jobtask.DeviceIDEQ(device)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row.ClaimAttempt != 2 || row.ClaimRunner != "r2" || row.ClaimOrderToken != 9 || row.ClaimOrderSource != "lease:e1" {
		t.Fatalf("the row holds attempt %d runner %q order %s:%d, want attempt 2 runner r2 order lease:e1:9",
			row.ClaimAttempt, row.ClaimRunner, row.ClaimOrderSource, row.ClaimOrderToken)
	}
}

// TestEntJobStore_ClaimBatch_ARaceWritesOneClaim: replicas consuming one job
// read an empty claim together, and their compare-and-set lets exactly one
// Runner take it. Every other batch is refused, not stored under a claim it
// lost.
func TestEntJobStore_ClaimBatch_ARaceWritesOneClaim(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)
	jobID, device := dispatchedDevice(t, store, client)
	const racers = 8
	var wg sync.WaitGroup
	results := make(chan string, racers)
	errs := make(chan error, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := facts.Claim{Attempt: 1, Runner: "runner-" + string(rune('a'+i)), Order: facts.Order{Source: "lease:e1", Token: 3}}
			refusal, err := store.ClaimBatch(ctx, jobID, device, next)
			if err != nil {
				errs <- err
				return
			}
			results <- refusal
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("a racing batch failed: %v", err)
	}
	accepted := 0
	for refusal := range results {
		if refusal == "" {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("%d batches took the claim, want exactly 1", accepted)
	}
	row, err := client.JobTask.Query().Where(jobtask.DeviceIDEQ(device)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row.ClaimRunner == "" || row.ClaimAttempt != 1 {
		t.Fatalf("the row holds attempt %d runner %q, want attempt 1 and one Runner", row.ClaimAttempt, row.ClaimRunner)
	}
}
