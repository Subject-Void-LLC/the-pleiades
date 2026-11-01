// Package dispatch: entJobStore's reads and writes for a job whose runbook
// has serial: (serial.go).
package dispatch

import (
	"context"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
)

// AssignBatches gives jobID's queued tasks their batches by device name, in
// one transaction, unless some task of the job already carries one. See
// JobStore.AssignBatches.
func (s *entJobStore) AssignBatches(ctx context.Context, jobID string, sizes engine.SerialList) error {
	row, err := s.jobRow(ctx, jobID)
	if err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin batching job %s: %w", jobID, err)
	}
	assigned, err := tx.JobTask.Query().Where(jobtask.HasJobWith(job.IDEQ(row.ID)), jobtask.BatchNotNil()).Exist(ctx)
	if err != nil {
		return rollback(tx, fmt.Errorf("failed to read job %s's batches: %w", jobID, err))
	}
	if assigned {
		// Already batched (a fan-out run again): nothing to write.
		_ = tx.Rollback()
		return nil
	}
	rows, err := tx.JobTask.Query().
		Where(jobtask.HasJobWith(job.IDEQ(row.ID)), jobtask.OutcomeEQ(jobtask.OutcomeDispatched), jobtask.WaitingEQ(true)).
		All(ctx)
	if err != nil {
		return rollback(tx, fmt.Errorf("failed to read job %s's queued devices: %w", jobID, err))
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].DeviceName < rows[j].DeviceName })
	for b, batch := range engine.Batches(rows, sizes) {
		ids := make([]int, len(batch))
		for i, r := range batch {
			ids[i] = r.ID
		}
		if err := tx.JobTask.Update().Where(jobtask.IDIn(ids...)).SetBatch(b).Exec(ctx); err != nil {
			return rollback(tx, fmt.Errorf("failed to record batch %d of job %s: %w", b+1, jobID, err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit job %s's batches: %w", jobID, err)
	}
	return nil
}

// SerialGate reads every batched task of jobID and decides the gate. See
// JobStore.SerialGate.
func (s *entJobStore) SerialGate(ctx context.Context, jobID string) (SerialGate, error) {
	rows, err := s.client.JobTask.Query().
		Where(jobtask.HasJobWith(job.JobIDEQ(jobID)), jobtask.BatchNotNil()).
		Select(jobtask.FieldBatch, jobtask.FieldOutcome, jobtask.FieldWaiting, jobtask.FieldResult).
		All(ctx)
	if err != nil {
		return SerialGate{}, fmt.Errorf("failed to read job %s's batches: %w", jobID, err)
	}
	gate := make([]SerialRow, 0, len(rows))
	for _, r := range rows {
		if r.Batch == nil {
			continue
		}
		running := r.Outcome == jobtask.OutcomeDispatched && (r.Waiting || r.Result == "")
		failed := r.Outcome == jobtask.OutcomeFailed || r.Result == jobtask.ResultFailed
		gate = append(gate, SerialRow{Batch: *r.Batch, Unfinished: running, Failed: failed})
	}
	return gateOf(gate), nil
}

// QueuedInBatch returns up to limit of jobID's queued tasks in batch, oldest
// first. See JobStore.QueuedInBatch.
func (s *entJobStore) QueuedInBatch(ctx context.Context, jobID string, batch, limit int) ([]JobTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.client.JobTask.Query().
		Where(
			jobtask.HasJobWith(job.JobIDEQ(jobID)),
			jobtask.OutcomeEQ(jobtask.OutcomeDispatched),
			jobtask.WaitingEQ(true),
			jobtask.BatchEQ(batch),
		).
		Order(ent.Asc(jobtask.FieldDeviceName)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read batch %d's queued devices on job %s: %w", batch+1, jobID, err)
	}
	tasks := make([]JobTask, 0, len(rows))
	for _, r := range rows {
		tasks = append(tasks, JobTask{DeviceID: r.DeviceID, DeviceName: r.DeviceName, Outcome: OutcomeQueued})
	}
	return tasks, nil
}
