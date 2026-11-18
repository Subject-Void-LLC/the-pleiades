// Package dispatch holds a device's facts-batch claim in its job record (Phase 41b): ClaimBatch is the one place the claim is read and written, as a compare-and-set.
package dispatch

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// claimAttempts bounds how often ClaimBatch re-reads a claim another replica
// wrote between its read and its write. Each lost race means the other side
// moved the claim, so a few tries settle it; past them the batch is redelivered.
const claimAttempts = 5

// ClaimBatch implements facts.ClaimStore over the job's record. It decides
// next against the claim the device's row holds (facts.DecideClaim) and writes
// the new claim only if the row still holds the claim it read, so two
// replicas cannot both take one place. A device the job's record lacks is an
// error, not a refusal: the bus redelivers until the dispatch is recorded.
func (s *entJobStore) ClaimBatch(ctx context.Context, jobID, deviceID string, next facts.Claim) (string, error) {
	for range claimAttempts {
		row, err := s.client.JobTask.Query().
			Where(jobtask.HasJobWith(job.JobIDEQ(jobID)), jobtask.DeviceIDEQ(deviceID)).
			First(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to read the claim of device %s on job %s: %w", deviceID, jobID, err)
		}
		held := facts.Claim{
			Attempt: row.ClaimAttempt,
			Runner:  row.ClaimRunner,
			Order:   facts.Order{Source: row.ClaimOrderSource, Token: row.ClaimOrderToken},
		}
		want, refusal := facts.DecideClaim(held, next)
		if refusal != "" {
			return refusal, nil
		}
		if want == held {
			return "", nil
		}
		n, err := s.client.JobTask.Update().
			Where(
				jobtask.ID(row.ID),
				jobtask.ClaimAttempt(held.Attempt),
				jobtask.ClaimRunner(held.Runner),
				jobtask.ClaimOrderSource(held.Order.Source),
				jobtask.ClaimOrderToken(held.Order.Token),
			).
			SetClaimAttempt(want.Attempt).
			SetClaimRunner(want.Runner).
			SetClaimOrderSource(want.Order.Source).
			SetClaimOrderToken(want.Order.Token).
			Save(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to write the claim of device %s on job %s: %w", deviceID, jobID, err)
		}
		if n == 1 {
			return "", nil
		}
	}
	return "", fmt.Errorf("the claim of device %s on job %s kept changing under the write", deviceID, jobID)
}

// Compile-time check that the dispatch store is the facts consumer's claim store.
var _ facts.ClaimStore = (*entJobStore)(nil)
