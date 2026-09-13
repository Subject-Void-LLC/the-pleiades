// Package journal: the Walk tier's durable store.
//
// The Crawl tier writes its journal to a file next to the runbook. The
// Walk tier's Runner publishes instead, and this is what the Controller
// puts on the other end of that subscription: rows in the one database
// the control plane already has.
//
// It is not an engine.Journal. The port's implementations are the two
// sinks a RUN writes through, and no run writes here: a Runner produced
// these entries on another machine and a Controller is storing what it
// was handed. Making this satisfy the same interface would invite
// somebody to wire it into an Executor, which would put a database
// dependency inside a process that has no database.
package journal

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entjournal "github.com/Subject-Void-LLC/the-pleiades/internal/ent/journalentry"
)

// ErrUnstorable marks a batch this store will never accept, however many
// times it is offered.
//
// The distinction it draws is the one a consumer has to make and cannot
// make from an error string. A database that is briefly unavailable is
// exactly what redelivery exists for, so that failure must be retried. A
// batch carrying a value no column can hold will be refused identically
// forever, so retrying it parks a poison message at the head of a
// consumer group and blocks every batch behind it.
//
// Found by this phase's own Schema and Injection Hardening audit, not by
// review: a deliberately malformed payload decoded cleanly into a Batch
// holding one entry with an empty outcome, which the store refused and
// the consumer then asked to have sent again, forever.
var ErrUnstorable = errors.New("the journal batch can never be stored")

// EntStore persists journal entries into the control plane's database.
type EntStore struct {
	// client is the control plane's own ent client, shared with every
	// other store rather than opened again here: this writes into the
	// same database, in the same process, as everything else the
	// Controller persists.
	client *ent.Client
}

// NewEntStore builds the ent-backed store over client.
func NewEntStore(client *ent.Client) *EntStore {
	return &EntStore{client: client}
}

// Save writes every entry, skipping any that is already recorded.
//
// A row is identified by (job_id, device_id, attempt, node_id), so a
// duplicate publish of one batch is not an error to report: it is the
// same fact arriving twice, and the unique index is what makes saying so
// cheap. A redelivered dispatch carries a different attempt and is
// therefore a new row, which is the point of recording the attempt at
// all.
//
// Entries are written one at a time rather than as a bulk insert. A bulk
// insert is one statement, so on Postgres a single conflicting row
// aborts the whole transaction and takes the entries that were fine with
// it; written singly, a duplicate skips and its neighbors land. A level
// is a handful of nodes, so the cost is a handful of statements.
func (s *EntStore) Save(ctx context.Context, entries []engine.JournalEntry) (int, error) {
	written := 0
	for _, entry := range entries {
		created, err := s.saveOne(ctx, entry)
		if err != nil {
			return written, err
		}
		if created {
			written++
		}
	}
	return written, nil
}

// saveOne writes one entry, reporting whether it was new.
func (s *EntStore) saveOne(ctx context.Context, entry engine.JournalEntry) (bool, error) {
	outcome, err := entOutcome(entry.Outcome)
	if err != nil {
		return false, err
	}

	create := s.client.JournalEntry.Create().
		SetJobID(entry.JobID).
		SetDeviceID(entry.DeviceID).
		SetAttempt(entry.Attempt).
		SetNodeID(entry.NodeID).
		SetRunID(entry.RunID).
		SetSequence(entry.Sequence).
		SetDagID(entry.DAGID).
		SetDagVersion(entry.DAGVersion).
		SetFqcn(entry.FQCN).
		SetFqcnUnresolved(entry.FQCNUnresolved).
		SetTaskName(entry.TaskName).
		SetRegister(entry.Register).
		SetOutcome(outcome).
		SetFailureStage(string(entry.FailureStage)).
		SetSkipKind(string(entry.SkipKind)).
		SetSkipOrdinal(entry.SkipOrdinal).
		SetSkipTotal(entry.SkipTotal).
		SetStatKeys(entry.StatKeys).
		SetUndeclaredStatCount(entry.UndeclaredStatCount).
		SetParamKeys(entry.ParamKeys).
		SetUndeclaredParamCount(entry.UndeclaredParamCount).
		SetInverseFqcn(entry.InverseFQCN).
		SetInverseFqcnUnresolved(entry.InverseFQCNUnresolved).
		SetInverseParamKeys(entry.InverseParamKeys).
		SetUndeclaredInverseParamCount(entry.UndeclaredInverseParamCount).
		SetDiffRecorded(entry.DiffRecorded)

	// The zero time is what a node that executed nothing carries, and
	// writing it would record 0001-01-01 as if it were an instant. The
	// column is nullable so the absence can be stored as an absence.
	if !entry.StartedAt.IsZero() {
		create = create.SetStartedAt(entry.StartedAt)
	}
	if !entry.FinishedAt.IsZero() {
		create = create.SetFinishedAt(entry.FinishedAt)
	}

	if _, err := create.Save(ctx); err != nil {
		if ent.IsConstraintError(err) {
			// Already recorded. Not an error: the identity is exactly
			// what the unique index names, so a second copy of one fact
			// is the constraint doing its job.
			return false, nil
		}
		return false, fmt.Errorf("journal: save entry %s of job %s: %w", entry.NodeID, entry.JobID, err)
	}
	return true, nil
}

// entOutcome maps the engine's outcome onto the column's own enum.
//
// It is an exhaustive switch that fails closed rather than a string
// conversion, for the same reason the projection that produced the value
// fails closed: a new engine outcome would otherwise reach the database
// as a value the column does not allow, and the first report of it would
// be a constraint error from a driver, naming neither the outcome nor
// the run it came from.
func entOutcome(outcome engine.Outcome) (entjournal.Outcome, error) {
	switch outcome {
	case engine.OutcomeRan:
		return entjournal.OutcomeRan, nil
	case engine.OutcomeChanged:
		return entjournal.OutcomeChanged, nil
	case engine.OutcomeSkipped:
		return entjournal.OutcomeSkipped, nil
	case engine.OutcomeFailed:
		return entjournal.OutcomeFailed, nil
	case engine.OutcomeNotReached:
		return entjournal.OutcomeNotReached, nil
	default:
		return "", fmt.Errorf("journal: unknown outcome %q, which this store has no column value for: %w", outcome, ErrUnstorable)
	}
}
