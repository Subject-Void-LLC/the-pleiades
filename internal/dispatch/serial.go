// Package dispatch: serial:, a runbook's devices run a batch at a time on the
// Controller, through the forks window's pump (window.go).
//
// A job whose runbook has serial: queues every admitted device during its
// fan-out, as a windowed job does, and once the fan-out is done each queued
// device is given its batch by device name (AssignBatches, the one
// engine.Batches both tiers use). The pump then dispatches only the lowest
// batch with a device still to finish, so the next batch starts when every
// device of the one before has reported. A batch in which a device failed
// stops the rollout: its own devices finish, and every device of the later
// batches is skipped, naming the batch that failed. That is the CLI's rule
// too, where a failure ends the batch's run.
//
// The forks window still bounds a batch: with forks set, at most that many of
// a batch's devices run at once; without it, the whole batch does.
package dispatch

import (
	"context"
	"fmt"
	"sort"
)

// SerialRow is what the serial gate reads of one batched task.
type SerialRow struct {
	// Batch is the task's batch, 0 for the first.
	Batch int
	// Unfinished is a device still to report: queued, or dispatched with no
	// result yet.
	Unfinished bool
	// Failed is a device whose dispatch failed or whose run reported a
	// failure.
	Failed bool
}

// SerialGate is where a job's serial: batches stand.
type SerialGate struct {
	// Batched says the job's tasks carry batches at all. A job without
	// serial: reads false, and the rest is empty.
	Batched bool
	// Current is the lowest batch with a device still to finish, and Size
	// how many devices it has.
	Current, Size int
	// Done says no batched device is left to finish.
	Done bool
	// StoppedBy is the batch whose failure stopped the rollout, or -1.
	StoppedBy int
}

// gateOf decides the gate from a job's batched rows: the lowest batch with an
// unfinished device is current, unless an earlier batch had a failure, which
// stops the rollout at that earlier batch.
func gateOf(rows []SerialRow) SerialGate {
	g := SerialGate{StoppedBy: -1}
	if len(rows) == 0 {
		return g
	}
	g.Batched = true
	failed := map[int]bool{}
	size := map[int]int{}
	current := -1
	for _, r := range rows {
		size[r.Batch]++
		if r.Failed {
			failed[r.Batch] = true
		}
		if r.Unfinished && (current == -1 || r.Batch < current) {
			current = r.Batch
		}
	}
	if current == -1 {
		g.Done = true
		return g
	}
	var earlier []int
	for b := range failed {
		if b < current {
			earlier = append(earlier, b)
		}
	}
	if len(earlier) > 0 {
		sort.Ints(earlier)
		g.StoppedBy = earlier[0]
	}
	g.Current, g.Size = current, size[current]
	return g
}

// serialStopReason is why a device of a later batch was not run.
func serialStopReason(batch int) string {
	return fmt.Sprintf("serial: batch %d had a failure, so the rollout stopped before this device's batch", batch+1)
}

// stopRollout skips every device of jobID still queued, the rollout having
// stopped at batch.
func (w *Worker) stopRollout(ctx context.Context, jobID string, batch int) error {
	for {
		queued, err := w.store.QueuedTasks(ctx, jobID, 100)
		if err != nil {
			return err
		}
		if len(queued) == 0 {
			return nil
		}
		moved := false
		for _, task := range queued {
			did, err := w.store.ResolveQueued(ctx, jobID, task.DeviceID, OutcomeSkipped, serialStopReason(batch))
			if err != nil {
				return err
			}
			moved = moved || did
		}
		// Another replica resolved them first; asking again would return the
		// same rows forever.
		if !moved {
			return nil
		}
	}
}

// serialGate reads job's gate, first batching its queued devices when the
// fan-out queued them for serial: and did not get to batch them (a failure
// between the two): an unwindowed job with queued devices is exactly that
// job, and without its batches its pump would wait forever.
func (w *Worker) serialGate(ctx context.Context, job *Job) (SerialGate, error) {
	gate, err := w.store.SerialGate(ctx, job.JobID)
	if err != nil || gate.Batched || windowOf(job) > 0 {
		return gate, err
	}
	queued, err := w.store.QueuedTasks(ctx, job.JobID, 1)
	if err != nil || len(queued) == 0 {
		return gate, err
	}
	def := w.pumpDefinitionFor(ctx, job)
	if def.reason != "" || len(def.prepared.Serial) == 0 {
		return gate, nil
	}
	if err := w.store.AssignBatches(ctx, job.JobID, def.prepared.Serial); err != nil {
		return gate, err
	}
	return w.store.SerialGate(ctx, job.JobID)
}
