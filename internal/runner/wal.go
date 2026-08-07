package runner

import (
	"context"
	"time"
)

// ResultEntry is one buffered execution outcome. ResultWAL.Append
// persists it durably before any network publish is attempted, so a
// crash between "the job finished" and "the outcome was reported" never
// silently loses it; a later call to Pending recovers it (from a fresh
// process, if the prior one crashed) and retries delivery until
// Acknowledge confirms it was actually accepted.
type ResultEntry struct {
	// ID identifies this entry. Assigned by Append if left empty, but a
	// caller that can derive a stable, natural key (agent_wal.go's
	// reportResult sets this to payload.JobID+":"+payload.DeviceID,
	// mirroring internal/dispatch/worker_devices.go's own identical key)
	// should always do so: ID also doubles as the outgoing publish's own
	// idempotency key (agent_wal.go's flushOne), so a redundant retry of
	// an already-delivered entry dedups server-side via the shared
	// event.Bus idempotency mechanism instead of reporting the same
	// outcome twice. A fresh random ID per Append call (Append's own
	// fallback for a caller with no natural key available) can only dedup
	// same-process retries of the identical already-Appended entry
	// (Pending returns it with its own already-assigned ID unchanged); it
	// cannot dedup two separate Append calls describing the logically
	// same outcome (e.g. an execution redelivered and re-run after a
	// crash), since each such call would mint its own distinct random ID
	// with nothing to recognize them as the same event.
	ID string `json:"id"`

	// JobID identifies the overall dispatch operation this outcome
	// belongs to (wire.DispatchPayload.JobID).
	JobID string `json:"job_id"`

	// DeviceID identifies the device this outcome is for
	// (wire.DispatchPayload.DeviceID).
	DeviceID string `json:"device_id"`

	// RunbookID names the runbook that was executed
	// (wire.DispatchPayload.RunbookID).
	RunbookID string `json:"runbook_id"`

	// Outcome is "completed" or "failed", mirroring dispatch.Outcome's
	// own vocabulary (internal/dispatch/job.go) without importing it: WAL
	// entries are a Runner-local concern with no compile-time dependency
	// on the Controller-side dispatch package.
	Outcome string `json:"outcome"`

	// Reason carries the execution error's own message when Outcome is
	// "failed". Empty when Outcome is "completed".
	Reason string `json:"reason,omitempty"`

	// RecordedAt is when Append persisted this entry, in UTC. Defaulted
	// by Append if left zero.
	RecordedAt time.Time `json:"recorded_at"`
}

// ResultWAL durably buffers ResultEntry values before Agent attempts to
// report them over the event bus, and lets a caller resume or retry
// delivery of everything not yet acknowledged. This is the Runner-local
// half of PLAN.md Section 16's State Desync Mitigation ("the Runner
// buffers the result in a local Write-Ahead Log (WAL) and retries"); the
// Controller-side half (treating a permanently lost result as "Unknown"
// and dispatching a Reconciliation runbook) has no consumer in this
// codebase yet and is not this interface's concern.
type ResultWAL interface {
	// Append durably persists entry, assigning entry.ID and
	// entry.RecordedAt if either is unset, and returns the persisted
	// entry (with those fields filled in). It must not return until
	// entry is durable: a crash immediately after Append returns must
	// never lose it.
	Append(ctx context.Context, entry ResultEntry) (ResultEntry, error)

	// Pending returns every entry not yet Acknowledge'd, including ones
	// Append'd by a prior process instance (a Runner restart), in the
	// order they were originally appended.
	Pending(ctx context.Context) ([]ResultEntry, error)

	// Acknowledge marks id as durably delivered, so it no longer appears
	// in a later Pending call. Acknowledging an id that is not currently
	// pending (already acknowledged, e.g. by a redundant flush racing a
	// concurrent one) is not an error: it is idempotent by design, since
	// the caller cannot always tell in advance which flush attempt will
	// win that race.
	Acknowledge(ctx context.Context, id string) error

	// Close releases any resources this ResultWAL holds open. It does
	// not delete or otherwise affect already-persisted entries.
	Close() error
}
