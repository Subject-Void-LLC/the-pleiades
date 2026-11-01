// Package journal: the reads a Walk-tier rollback plans from (Phase 40).
package journal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entjournal "github.com/Subject-Void-LLC/the-pleiades/internal/ent/journalentry"
)

// MaxRollbackEntries bounds what one rollback reads: the undone job's
// entries, and separately the entries other jobs wrote on its devices
// since. A rollback plans from the whole of both, so a read past this is
// refused rather than cut short: a plan made from part of a journal could
// miss a change, or a later job's, and look complete.
const MaxRollbackEntries = 100_000

// ErrTooMuchToPlan is returned when a rollback's reads pass
// MaxRollbackEntries.
var ErrTooMuchToPlan = errors.New("journal: more entries than one rollback plans from")

// deviceChunk bounds one IN list, well under SQLite's limit on bound
// parameters.
const deviceChunk = 500

// AllForJob returns every entry job jobID wrote, in device, attempt and
// sequence order, or ErrTooMuchToPlan.
func (s *EntStore) AllForJob(ctx context.Context, jobID string) ([]engine.JournalEntry, error) {
	return s.allForJob(ctx, jobID, MaxRollbackEntries)
}

// allForJob is AllForJob with its bound as a parameter.
func (s *EntStore) allForJob(ctx context.Context, jobID string, limit int) ([]engine.JournalEntry, error) {
	rows, err := s.client.JournalEntry.Query().
		Where(entjournal.JobIDEQ(jobID)).
		Order(ent.Asc(entjournal.FieldDeviceID), ent.Asc(entjournal.FieldAttempt), ent.Asc(entjournal.FieldSequence)).
		Limit(limit + 1).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("journal: reading the entries of job %q: %w", jobID, err)
	}
	if len(rows) > limit {
		return nil, fmt.Errorf("%w: job %s wrote more than %d", ErrTooMuchToPlan, jobID, limit)
	}
	entries := make([]engine.JournalEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, hydrateEntry(row))
	}
	return entries, nil
}

// OnDevicesSince returns every entry a job other than jobID wrote on one
// of devices that could have changed them after jobID began: every such
// job, its rollbacks included, and jobID's own rollbacks. Or
// ErrTooMuchToPlan.
//
// fromRevision is the lowest lease revision jobID's own entries record
// (lock.Fenced), zero when they record none. A row carrying a revision is
// selected when its revision is above fromRevision, whatever its times say,
// since two Runners' clocks can disagree; the planner then compares each
// device's revisions exactly (internal/rollback's order.go). A row from
// before revisions, and every row when jobID has none, is selected as it
// always was: finished after since.
func (s *EntStore) OnDevicesSince(ctx context.Context, jobID string, devices []string, since time.Time, fromRevision uint64) ([]engine.JournalEntry, error) {
	return s.onDevicesSince(ctx, jobID, devices, since, fromRevision, MaxRollbackEntries, deviceChunk)
}

// onDevicesSince is OnDevicesSince with its bound and IN-list size as
// parameters.
func (s *EntStore) onDevicesSince(ctx context.Context, jobID string, devices []string, since time.Time, fromRevision uint64, limit, size int) ([]engine.JournalEntry, error) {
	// Which rows could be later than jobID: by revision where both carry one,
	// by clock where either does not.
	later := entjournal.FinishedAtGT(since)
	if fromRevision != 0 {
		later = entjournal.Or(
			entjournal.LeaseRevisionGT(fromRevision),
			entjournal.And(entjournal.LeaseRevisionEQ(0), entjournal.FinishedAtGT(since)),
		)
	}
	var entries []engine.JournalEntry
	for start := 0; start < len(devices); start += size {
		chunk := devices[start:min(start+size, len(devices))]
		rows, err := s.client.JournalEntry.Query().
			Where(
				entjournal.JobIDNEQ(jobID),
				entjournal.DeviceIDIn(chunk...),
				later,
			).
			Order(ent.Asc(entjournal.FieldJobID), ent.Asc(entjournal.FieldDeviceID), ent.Asc(entjournal.FieldAttempt), ent.Asc(entjournal.FieldSequence)).
			Limit(limit + 1 - len(entries)).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("journal: reading what other jobs did on job %q's devices: %w", jobID, err)
		}
		for _, row := range rows {
			entries = append(entries, hydrateEntry(row))
		}
		if len(entries) > limit {
			return nil, fmt.Errorf("%w: other jobs wrote more than %d on job %s's devices since it began", ErrTooMuchToPlan, limit, jobID)
		}
	}
	return entries, nil
}
