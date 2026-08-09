// Package dispatch is the durable, asynchronous fan-out worker
// PLAN.md Section 28.4 requires: a launch persists a Job with a stored
// selector and returns 202 Accepted immediately, and a durable worker
// (Worker, in worker.go) performs the actual per-device fan-out later, off
// the HTTP request path entirely.
//
// This replaces the old synchronous in-HTTP-handler loop
// (internal/api/dispatcher.go's DispatchRunbook), which streamed the
// whole target group and published one event per device inside a single
// HTTP request, meaning a 10,000-device dispatch held the request open
// for as long as the slowest publish and left no durable record if the
// process died mid-loop. Under this package's design, DispatchRunbook's
// replacement (a later stage in this session, not this file) only ever
// creates a Job row and publishes one job.requested event; every actual
// per-device decision happens here, in a background worker that survives
// a process restart because its progress lives in Postgres, not in a
// request goroutine's stack.
//
// Like pkg/inventory.InventoryItem never leaking an ent row across the
// Repository port, this package's own port (JobStore, below) never leaks
// a generated ent.* type across its boundary either: Job and JobTask here
// are plain domain structs, and ent_store.go is the one place that
// translates between them and the generated internal/ent types.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Job is the domain view of one asynchronous dispatch request: a runbook
// requested against a target group, and the terminal tallies once fan-out
// finishes. It is deliberately narrower than the generated ent.Job (no
// internal auto-increment ID, no ent edges), the same "storage-agnostic
// domain type" shape internal/inventory/record.Record gives
// pkg/inventory.InventoryItem.
//
// It deliberately carries no FailureReason field: a "failed" State (see
// State's own doc comment) is already genuinely, non-silently visible and
// distinguishable from "completed", which is the honesty requirement this
// package's own schema change exists to satisfy. The human-readable reason
// text itself is persisted (internal/ent/schema/job.go's failure_reason
// column, written by JobStore.Fail) but is not yet plumbed through this
// struct, since no caller of JobStore.Get needs it in this phase; a later
// stage that does can widen this struct then, the same incremental way
// every other field here earned its place.
type Job struct {
	// JobID is the stable, opaque identifier a caller polls on.
	JobID string
	// RunbookID names the runbook this job dispatches.
	RunbookID string
	// GroupName restricts the dispatch to devices in the named group. An
	// empty GroupName means no restriction, mirroring
	// pkg/inventory.Selector.GroupName's own documented zero value.
	GroupName string
	// Actor is the identity subject that requested the job, stamped once
	// at creation. The Worker publishes every per-device dispatch event
	// with this Actor, since there is no live HTTP caller identity left
	// by the time a background worker picks the job up.
	Actor string
	// State is the job's current lifecycle position: "pending",
	// "fanning_out", "completed", or "failed" (see
	// internal/ent/schema/job.go's State field for why "failed" exists
	// as its own state rather than being folded into "completed").
	State string
	// DispatchedCount, SkippedCount, and FailedCount are the terminal
	// per-device tallies, final once State is "completed". They read 0
	// before that, regardless of how much fan-out work has actually
	// happened; a caller wanting live progress reads the JobTask list
	// Get also returns.
	DispatchedCount int
	SkippedCount    int
	FailedCount     int
	// CreatedAt is when the job was first persisted.
	CreatedAt time.Time
}

// JobTask is the domain view of one device's outcome within a Job's
// fan-out: dispatched, skipped (with a reason), or failed (with a
// reason).
type JobTask struct {
	// DeviceID is the target device's stable opaque identifier, captured
	// at dispatch time rather than linked by a live reference, so a job's
	// history stays readable even if the device is later renamed or
	// removed from inventory.
	DeviceID string
	// DeviceName is the target device's display name at dispatch time.
	DeviceName string
	// Outcome is this task's result: OutcomeDispatched, OutcomeSkipped,
	// or OutcomeFailed.
	Outcome Outcome
	// Reason explains a skipped or failed outcome. It must only ever name
	// a device (its Name), its lifecycle State, or a missing
	// capability.Name, mirroring internal/ent/schema/job_task.go's own
	// field comment: this is an audit trail, and a device's Properties()
	// value is never safe to echo into it.
	Reason string
}

// Outcome is the fixed set of results a JobTask can record. It is a named
// string type with a String method and a parser that rejects an
// unrecognized value, the same shape
// pkg/inventory.LifecycleState/ParseLifecycleState uses and for the same
// reason stated there: a value this build does not recognize must never be
// silently coerced into one that does, since that could misrepresent a
// failed dispatch as a successful one or vice versa.
type Outcome string

// The three outcomes a JobTask can record.
const (
	// OutcomeDispatched means the runbook was successfully handed off to
	// the event bus for this device.
	OutcomeDispatched Outcome = "dispatched"
	// OutcomeSkipped means the device was deliberately excluded (a
	// lifecycle state that cannot execute, a missing required
	// capability, or a missing host property), never attempted.
	OutcomeSkipped Outcome = "skipped"
	// OutcomeFailed means dispatch was attempted for this device and did
	// not succeed (e.g. the event bus publish returned an error).
	OutcomeFailed Outcome = "failed"
)

// String renders the outcome for storage and log lines.
func (o Outcome) String() string {
	return string(o)
}

// ParseOutcome is the inverse of String, for adapters hydrating a stored
// outcome back into the domain. It returns an error on an unrecognized
// value rather than defaulting to any particular outcome: mirroring
// pkg/inventory.ParseLifecycleState's own reasoning, silently promoting an
// unknown stored value into, say, OutcomeDispatched would misreport a
// device this build cannot explain as having succeeded.
func ParseOutcome(s string) (Outcome, error) {
	switch Outcome(s) {
	case OutcomeDispatched:
		return OutcomeDispatched, nil
	case OutcomeSkipped:
		return OutcomeSkipped, nil
	case OutcomeFailed:
		return OutcomeFailed, nil
	default:
		return "", fmt.Errorf("unrecognized job task outcome: %q", s)
	}
}

// JobStore is the persistence port this package depends on for Job and
// JobTask records. entJobStore (ent_store.go) is the one real
// implementation, backed by the Job and JobTask ent schemas
// (internal/ent/schema/job.go, job_task.go).
type JobStore interface {
	// Create persists a brand new job. Callers set every field on job
	// except CreatedAt (stamped by the store) and State (always starts
	// "pending"); job.State is ignored on input.
	Create(ctx context.Context, job *Job) error

	// Get returns the job identified by jobID together with every JobTask
	// recorded against it so far, or ErrJobNotFound if no such job
	// exists.
	Get(ctx context.Context, jobID string) (*Job, []JobTask, error)

	// BeginFanOut atomically transitions job jobID from "pending" to
	// "fanning_out", or reclaims a job already in "fanning_out" whose
	// updated_at has not advanced in at least staleAfter, implemented as
	// one WHERE-guarded conditional ent bulk update rather than a
	// read-then-write pair, so two concurrent or redelivered callers can
	// never both believe they own the fan-out.
	//
	// The staleAfter reclaim exists for the crash/restart case a plain
	// pending-only guard cannot recover from: a Worker that dies after
	// this call returns true but before Complete or Fail runs would
	// otherwise leave jobID parked in "fanning_out" forever, since every
	// future redelivery of the same job.requested message would see
	// began == false and skip without doing any work. RecordTask refreshes
	// jobID's updated_at on every device it records, so a job a Worker is
	// genuinely still, actively fanning out for never looks stale; only a
	// job whose last recorded progress is older than staleAfter is
	// eligible for reclaim.
	//
	// It returns (true, fence, nil) when THIS call performed that
	// transition, whether the normal pending claim or a stale reclaim:
	// the caller is now the fan-out's sole owner and must carry fence
	// through to every subsequent RecordTask, Complete, or Fail call for
	// this claim. fence is the job's fencing token value as stored
	// immediately after this call's own atomic increment (see
	// internal/ent/schema/job.go's fence field comment): it is
	// monotonically higher than any fence value a prior claimant of this
	// same job could have obtained, which is what lets the storage layer
	// reject a superseded caller's later writes rather than merely
	// hoping it notices and stops on its own. Because a reclaim re-runs
	// the whole fan-out loop from the start, the caller
	// (Worker.HandleJobRequested) must treat any device already present
	// in the JobTask list Get returns as already handled and must not
	// call RecordTask, admit, or dispatch to it a second time; RecordTask
	// itself performs no such check, so skipping it is entirely the
	// caller's responsibility. It returns (false, 0, nil), with no
	// error, when jobID is "completed" or "failed", or is "fanning_out"
	// but not yet stale (another delivery is plausibly still actively
	// handling it): the caller must treat this as already handled and
	// must not reprocess it, and the returned fence is meaningless (0)
	// since no claim was made. It returns a non-nil error, wrapping
	// ErrJobNotFound, only when jobID names no job at all.
	BeginFanOut(ctx context.Context, jobID string, staleAfter time.Duration) (claimed bool, fence int64, err error)

	// ListStaleFanOuts returns the JobID of every job currently
	// "fanning_out" whose updated_at has not advanced in at least
	// staleAfter, the identical predicate BeginFanOut's own reclaim branch
	// evaluates, exposed here as a read-only scan rather than a claim.
	//
	// This exists because BeginFanOut's reclaim, on its own, is reachable
	// only from inside Worker.HandleJobRequested, which only runs in
	// response to a job.requested delivery. A Worker that dies between
	// BeginFanOut and Complete/Fail leaves nothing to ever deliver that
	// event again: JetStream's redelivery budget (MaxDeliverDefault
	// redeliveries at consumerAckWait apart, internal/topology) is far
	// shorter than any realistic staleAfter, so the message dead-letters
	// long before the job becomes eligible for reclaim, and BeginFanOut's
	// reclaim branch is provably never reached by natural redelivery
	// alone. ListStaleFanOuts is what a periodic reaper (Reaper,
	// reaper.go) calls to find a job in that state and re-publish
	// job.requested for it itself, giving BeginFanOut's own,
	// already-correct reclaim logic a trigger that does not depend on
	// NATS ever redelivering anything.
	//
	// Callers must pass the same staleAfter a real BeginFanOut(ctx, id,
	// staleAfter) call would use for these jobs' own Worker, or this scan
	// and that claim can disagree about which jobs are actually eligible.
	ListStaleFanOuts(ctx context.Context, staleAfter time.Duration) ([]string, error)

	// RecordTask persists task as jobID's outcome for one device and
	// refreshes jobID's own updated_at timestamp, the heartbeat
	// BeginFanOut's staleAfter reclaim reads to tell a job a Worker is
	// still actively making progress on apart from one that has stalled.
	// Called once per device considered during fan-out, regardless of
	// outcome. fence must be the value the caller's own BeginFanOut call
	// most recently returned; the write is conditioned on jobID's stored
	// fence still matching, so a caller superseded by a later reclaim
	// (whose own BeginFanOut bumped the stored fence past what this
	// caller holds) gets ErrFenced back instead of silently succeeding.
	// It performs no idempotency check of its own beyond that: a caller
	// that might call it twice for the same (jobID, DeviceID), i.e. a
	// Worker carrying out BeginFanOut's stale reclaim, must consult
	// Get's own returned task list itself and never call RecordTask for
	// a device already present there. See BeginFanOut's own doc comment
	// for why that guard lives in the caller rather than here.
	RecordTask(ctx context.Context, jobID string, fence int64, task JobTask) error

	// Complete transitions jobID to "completed" and stamps the three
	// final tallies, the terminal write of a successful fan-out. fence
	// must be the value the caller's own BeginFanOut call most recently
	// returned; the write is conditioned on jobID's stored state still
	// being "fanning_out" and its stored fence still matching, so a
	// caller superseded by a later reclaim gets ErrFenced back instead of
	// overwriting a state a newer claimant (or an even later Complete or
	// Fail) already wrote. A second Complete or Fail call for the same
	// jobID, from any source, is rejected this same way rather than
	// silently succeeding a second time.
	Complete(ctx context.Context, jobID string, fence int64, dispatched, skipped, failed int) error

	// Fail transitions jobID to "failed" and stamps reason, the terminal
	// write when fan-out could not even begin (or could not finish)
	// because of a real, permanent error, e.g. the job's own RunbookID
	// resolves to no runbook this Controller can find. See
	// internal/ent/schema/job.go's State field comment for why "failed"
	// is a distinct state from "completed" rather than a "completed" job
	// with all-zero tallies, which would be indistinguishable from a
	// dispatch that legitimately ran against an empty group. fence must
	// be the value the caller's own BeginFanOut call most recently
	// returned; see Complete's own doc comment for the identical
	// state-plus-fence guard this write is conditioned on and why.
	Fail(ctx context.Context, jobID string, fence int64, reason string) error
}

// ErrJobNotFound is returned by JobStore methods when jobID names no job
// this store can resolve. Callers should use errors.Is(err,
// ErrJobNotFound) rather than comparing errors directly, since every real
// implementation wraps this rather than returning it bare.
var ErrJobNotFound = errors.New("job not found")

// ErrFenced is returned by RecordTask, Complete, and Fail when jobID names
// a real job but the fence value the caller presented no longer matches
// the job's stored fence. This means a later BeginFanOut call (a stale
// reclaim, see BeginFanOut's own doc comment) has already superseded the
// caller's claim on this job's fan-out: the caller is no longer the
// fan-out's owner and must stop making further calls for this claim
// immediately, not retry, since retrying can never make a stale fence
// current again. Callers should use errors.Is(err, ErrFenced) rather than
// comparing errors directly, since every real implementation wraps this
// rather than returning it bare.
var ErrFenced = errors.New("job fan-out ownership was reclaimed by another worker")
