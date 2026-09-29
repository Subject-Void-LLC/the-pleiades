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

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
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
	//
	// SUPERSEDED by InventoryID as of Phase 21, and written empty by every
	// remaining path. See internal/ent/schema/job.go's own field for why
	// the column stays rather than being dropped.
	GroupName string

	// InventoryID is what this dispatch targets: the shareable device set
	// the template names. The Worker streams its membership, which is its
	// groups plus the devices attached to it directly.
	//
	// Its zero value does NOT mean "everything", unlike GroupName's above,
	// and that inversion is the point. An inventory that selects nothing
	// must dispatch to nothing: pkg/inventory.Selector.Membership is a
	// pointer for exactly this reason, and a job carrying no inventory is
	// refused rather than fanned out to the fleet.
	InventoryID int

	// TemplateID and TemplateName record which saved definition this job
	// came from. The name is captured beside the id because a template's
	// job history has to outlive the template.
	TemplateID   int
	TemplateName string

	// LaunchConfigID names the stored configuration this job was launched
	// with: the overrides and survey answers the caller supplied. Zero
	// means the launch supplied nothing, so there was nothing to record.
	//
	// It is what a relaunch reads to repeat the run rather than merely
	// re-running the template as saved.
	LaunchConfigID int

	// Kind is the launch kind this job ran as, and therefore which
	// execution adapter handles it. It travels to the Runner on the
	// dispatch payload; it is recorded here so a job record says what it
	// was without joining back to a template that may since be gone.
	Kind string

	// OrganizationID is the tenancy boundary this dispatch happened
	// inside, taken from the template's inventory at launch.
	//
	// Zero means unscoped, which is what a job created by a path naming no
	// template would carry. Every path that exists today writes it, which
	// is what finally gives the column a writer.
	OrganizationID int
	// Actor is the identity subject that requested the job, stamped once
	// at creation. The Worker publishes every per-device dispatch event
	// with this Actor, since there is no live HTTP caller identity left
	// by the time a background worker picks the job up.
	Actor string
	// State is the job's current lifecycle position: "pending",
	// "fanning_out", "completed", "failed", or "canceled" (see
	// internal/ent/schema/job.go's State field for why "failed" exists
	// as its own state rather than being folded into "completed", and
	// why "canceled" is its own state rather than a kind of "failed").
	//
	// "running" is written when a fan-out that dispatched at least one
	// device settles (SettleRunning), and the job moves on to "completed"
	// once every dispatched device has reported (CompleteRunning). The
	// schema field's own comment predates that writer and still says
	// nothing writes it.
	State string
	// DispatchedCount, SkippedCount, and FailedCount are the per-device
	// fan-out tallies. They read 0 while the fan-out is still going and are
	// written, final, when it settles: into "running", "completed" or
	// "canceled". A caller wanting live progress during the fan-out reads
	// the JobTask list Get also returns.
	DispatchedCount int
	SkippedCount    int
	FailedCount     int
	// FailureReason explains a "failed" State: why the fan-out could not
	// begin, or could not finish. Empty for every other state.
	//
	// Plumbed through as of Phase 21, which this struct's own doc comment
	// anticipated ("a later stage that does can widen this struct then").
	// The stage arrived because a job can now fail for reasons an operator
	// has to act on and cannot guess: the inventory it targets was
	// deleted, or contains no devices. Without this the job record said
	// "failed" and nothing else, and the explanation existed only in a log
	// line on whichever replica happened to run the fan-out.
	//
	// It carries only sanitised text. internal/ent/schema/job.go's own
	// field states the rule: it must name a job's own inputs and never
	// echo a storage error, which can carry a table name, a column or a
	// host.
	FailureReason string

	// CanceledAt and CanceledBy record who stopped this job and when, and
	// are the zero value for every State other than "canceled".
	//
	// Kept apart from FailureReason rather than folded into it, because
	// the two answer different questions: FailureReason says why the
	// platform could not proceed, and these say which person decided it
	// should not. CanceledBy holds that person's identity subject, which
	// is often not Actor: a scheduled job is launched by the scheduler and
	// stopped by whoever was watching it.
	CanceledAt time.Time
	CanceledBy string

	// CreatedAt is when the job was first persisted.
	CreatedAt time.Time

	// Fields is the resolved launch.Resolved.Fields this job was
	// dispatched with: limit, verbosity, forks, timeout, and whichever
	// kind-specific fields its kind declares. Stamped once at creation and
	// never changes.
	//
	// Captured on the record before it reaches the wire or either adapter
	// (internal/adapters/legacy's argv construction and
	// internal/adapters/native's extra-variable injection are a separate,
	// not-yet-built consumer of this same data), so the job record is
	// honest about what a launch was configured with even before that
	// phase lands. See AWX_PARITY_ROADMAP.md.
	Fields launch.Fields

	// ExtraVars is the resolved launch.Resolved.ExtraVars this job was
	// dispatched with: the template's defaults, a saved configuration,
	// survey answers, and this launch's own overrides, already merged in
	// that precedence order. Same capture-now, consume-later status as
	// Fields above.
	ExtraVars map[string]any

	// CredentialIDs are the credentials this job's template was bound to
	// when it was launched, in binding order. Ids and nothing else; see
	// internal/ent/schema/job.go's own field for what is deliberately not
	// recorded beside them.
	//
	// This is what the fan-out resolves and injects, and it is the audit
	// answer to what a run authenticated as.
	CredentialIDs []int

	// ExternalChecks is whether whoever launched this job may run it for
	// real, which is what lets a check of it run an external program's
	// Check (wire.DispatchPayload.ExternalChecks). False unless the launch
	// set it.
	ExternalChecks bool
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
	// Result, ResultReason and FinishedAt record what the Runner made of
	// this device once the runbook actually ran on it, which is a
	// different fact from Outcome above: that one says whether the
	// fan-out handed the device off, this one says what happened next.
	//
	// All three are the zero value until a result comes back, and stay
	// that way forever for a device that was skipped or whose dispatch
	// failed. ResultReason carries the same obligation Reason does: it
	// crosses the mesh from a Runner into an audit trail, so it must
	// never echo a device property or a raw internal error.
	Result       Result
	ResultReason string
	FinishedAt   time.Time

	// Unchecked is how many tasks a check could not check on this device,
	// as its Runner reported with the result: zero for a real run, for a
	// check that answered for every task, and until a result arrives.
	Unchecked int
}

// Result is the fixed set of execution outcomes a Runner reports back for
// one device, as distinct from the Outcome the Controller recorded when it
// dispatched.
//
// Named, parsed and rejected on an unrecognized value for the identical
// reason Outcome is: silently coercing a value this build does not know
// would be free to turn a failed run into a successful-looking one.
type Result string

// The two results a Runner can report, plus the empty value meaning it has
// not reported yet.
const (
	// ResultPending is the zero value: this device has not reported back.
	// A job with any task in this state is still running.
	ResultPending Result = ""
	// ResultSucceeded means the runbook ran on this device and finished
	// without error.
	ResultSucceeded Result = "succeeded"
	// ResultFailed means the runbook ran on this device and did not
	// finish successfully.
	ResultFailed Result = "failed"
)

// String renders the result for storage and log lines.
func (r Result) String() string {
	return string(r)
}

// ParseResult is the inverse of String, for the result consumer hydrating
// what a Runner put on the wire. It rejects an unrecognized value rather
// than defaulting, mirroring ParseOutcome exactly.
//
// The empty string is deliberately NOT accepted here. A result arriving
// over the mesh that names no outcome is malformed, and treating it as
// "not reported yet" would let a bad message quietly leave a job running
// forever while looking like it had been handled.
func ParseResult(s string) (Result, error) {
	switch Result(s) {
	case ResultSucceeded:
		return ResultSucceeded, nil
	case ResultFailed:
		return ResultFailed, nil
	default:
		return "", fmt.Errorf("unknown job task result: %q", s)
	}
}

// Outcome is the fixed set of results a JobTask can record. It is a named
// string type with a String method and a parser that rejects an
// unrecognized value, the same shape
// pkg/inventory.LifecycleState/ParseLifecycleState uses and for the same
// reason stated there: a value this build does not recognize must never be
// silently coerced into one that does, since that could misrepresent a
// failed dispatch as a successful one or vice versa.
type Outcome string

// The outcomes a JobTask can record. A row in an unwindowed job is
// written once, with one of the last three. A windowed job (a forks limit,
// window.go) first records an admitted device as OutcomeQueued, and that
// row later moves, once, to one of the other three.
const (
	// OutcomeQueued means a windowed job admitted the device and it is
	// waiting for a free place in the job's forks window. It has not been
	// handed to a Runner.
	OutcomeQueued Outcome = "queued"
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
	case OutcomeQueued:
		return OutcomeQueued, nil
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

	// List returns up to limit jobs, newest first, resuming after the
	// given cursor (a job id, or empty for the first page).
	//
	// It carries no JobTask rows. A list view does not display per-device
	// outcomes, and loading them for every job would be a query per row
	// for data nothing renders -- the same list-view contract
	// inventory.Repository.GetGroup already documents for device history.
	//
	// Ordering is newest-first on the job id, which is not an arbitrary
	// choice of column: the schema defaults it to a UUIDv7, so it is
	// time-ordered, unique and indexed, making it both the natural
	// recency sort and a valid keyset cursor with no second index and no
	// tiebreaker. Paging on created_at alone would need one, since two
	// jobs launched in the same instant would share a cursor and each
	// page boundary could then drop or repeat one.
	List(ctx context.Context, after string, limit int) ([]*Job, error)

	// ListForTemplate returns the jobs one template launched, newest
	// first, bounded by limit.
	//
	// Its own method rather than a filter on List, because it answers a
	// different question with a different index: List pages the whole
	// history on the job id, while this is the "what has this template
	// run" a template's detail page asks, served by the template_id index
	// the Job schema declares for exactly this. Filtering a page of the
	// global list in Go would scan the fastest-growing table here to find
	// a handful of rows.
	//
	// A template that has never run returns no jobs and no error, which is
	// an ordinary state rather than a missing record: this port does not
	// know whether a template exists.
	ListForTemplate(ctx context.Context, templateID, limit int) ([]*Job, error)

	// RecentForTemplates is ListForTemplate batched across many templates at
	// once, for a template list page's Activity and Last Ran columns.
	//
	// Its own method rather than a loop calling ListForTemplate per row, for
	// the reason ListForTemplate itself exists over List: a page of fifty
	// templates has no business costing fifty queries for data that renders
	// two columns. A template with no jobs is simply absent from the
	// returned map rather than present with an empty slice, so a caller's
	// membership check is one map lookup.
	RecentForTemplates(ctx context.Context, templateIDs []int, perTemplate int) (map[int][]*Job, error)

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

	// SettleRunning ends a fan-out that dispatched to at least one device
	// by moving jobID from "fanning_out" to "running" and stamping its
	// tallies, rather than straight to "completed".
	//
	// It is Complete's sibling and takes the identical guard. Which of the
	// two the worker calls is decided by one question: did anything get
	// handed to a Runner. If nothing did, every device having been skipped
	// or failed at dispatch, the job is genuinely over and Complete is
	// right. If something did, the job is not over just because the
	// Controller has stopped talking, and calling it completed would be
	// the platform reporting the end of its OWN work as the end of the
	// run.
	SettleRunning(ctx context.Context, jobID string, fence int64, dispatched, skipped, failed int) error

	// RecordResult records what a Runner made of one device, and reports
	// whether that was the last device the job was waiting on.
	//
	// It is idempotent by construction: a redelivered result writes the
	// same values again, and the "was that the last one" answer is taken
	// from the stored rows rather than from a counter, so a duplicate
	// cannot move a job to a terminal state twice or push a tally past
	// what actually happened.
	//
	// A result for a device this job never dispatched to, or for an
	// unknown job, returns ErrJobNotFound: both mean the same thing to the
	// consumer, which is that there is nothing here to record and
	// retrying will not change that.
	//
	// unchecked is how many tasks a check could not check on the device
	// (zero for a real run), stored with the result.
	RecordResult(ctx context.Context, jobID, deviceID string, result Result, reason string, unchecked int) (complete bool, err error)

	// CompleteRunning moves jobID from "running" to "completed" once every
	// dispatched device has reported. It is the only writer of that
	// transition.
	//
	// Separate from RecordResult rather than folded into it, so that the
	// decision to end a job is one conditional write that either matches a
	// running job or does nothing at all. Two results arriving at once can
	// therefore both believe they were last, and only one of them will
	// actually end the job.
	CompleteRunning(ctx context.Context, jobID string) error

	// Cancel stops jobID, moving it from "pending", "fanning_out" or
	// "running" to "canceled" and stamping canceledBy and the current time. It is a
	// compare-and-swap on the state alone, deliberately taking no fence:
	// a person pressing cancel is not a participant in the fan-out lease
	// and holds no claim to present, and requiring one would mean the only
	// party able to stop a job is the worker running it.
	//
	// It returns ErrJobNotFound if jobID names no job, and ErrNotCancelable
	// if it names one that has already reached a terminal state. Cancelling
	// an already-canceled job is therefore also ErrNotCancelable rather
	// than a second successful cancel, which keeps canceled_by honest about
	// who actually stopped it.
	//
	// Cancel settles the RECORD. It does not itself reach a Runner already
	// executing a dispatch for this job: that is a separate, best-effort
	// signal (see internal/topology.ControlSubject). What this guarantees
	// is the durable half, that no device this job has not yet reached will
	// be dispatched to, which the fan-out loop enforces by way of
	// RecordTask returning ErrCanceled.
	Cancel(ctx context.Context, jobID string, canceledBy string) error

	// SettleCanceled stamps the tallies a fan-out had reached at the
	// moment it was stopped, on a job already in the "canceled" state. It
	// changes no state: Cancel already did that, and this only fills in
	// what the worker had managed before it found out.
	//
	// It exists because the tallies are shown on a job's own record and
	// in the job list. Without it a job canceled after three hundred
	// devices had been dispatched to would report zero of everything
	// forever, since Complete, the only writer of those columns, never
	// runs for a canceled job. Zero is the truthful answer only for a job
	// canceled before any worker claimed it.
	//
	// Guarded on state and fence together, like Complete and Fail: the
	// caller is the worker that was performing the fan-out and must still
	// hold its claim, and a job that has somehow left "canceled" is not
	// one whose tallies this call should be writing.
	SettleCanceled(ctx context.Context, jobID string, fence int64, dispatched, skipped, failed int) error

	// Lookup returns jobID's record without its tasks, for a caller that
	// needs the job alone once per device result: a windowed job's pump.
	// Get loads every task, which for a large job would make each result
	// cost a read of the whole job.
	Lookup(ctx context.Context, jobID string) (*Job, error)

	// QueuedTasks returns up to limit of jobID's queued tasks, the oldest
	// first, which is the order they were admitted in.
	QueuedTasks(ctx context.Context, jobID string, limit int) ([]JobTask, error)

	// HeldSlots returns the forks-window slots jobID's dispatched devices
	// hold while they run (window.go).
	HeldSlots(ctx context.Context, jobID string) ([]int, error)

	// ClaimQueued moves jobID's queued task for deviceID to dispatched,
	// holding slot, and counts it in the job's dispatched tally. It
	// reports ClaimSlotTaken when another device holds slot (another
	// replica's pump got there first), and ClaimGone when the task is no
	// longer queued or the job is no longer running. It is called BEFORE
	// the device's dispatch is published, so a result can never arrive for
	// a device whose row does not yet say dispatched.
	ClaimQueued(ctx context.Context, jobID, deviceID string, slot int) (ClaimResult, error)

	// ResolveQueued moves jobID's queued task for deviceID to outcome
	// (OutcomeSkipped or OutcomeFailed) with reason, and counts it in the
	// matching tally. It reports false when the task was no longer queued.
	ResolveQueued(ctx context.Context, jobID, deviceID string, outcome Outcome, reason string) (bool, error)

	// ReleaseClaim moves a device ClaimQueued dispatched, whose publish
	// then failed, to failed with reason, frees its slot, and moves it from
	// the dispatched tally to the failed one.
	ReleaseClaim(ctx context.Context, jobID, deviceID, reason string) error

	// ListQueuedJobs returns every running job that still has a queued
	// task, for the leader's sweep to pump in case the pump that should
	// have followed a result never ran.
	ListQueuedJobs(ctx context.Context) ([]string, error)

	// CompleteIfDone ends a running job that has no queued task and no
	// dispatched device still out, and does nothing otherwise.
	CompleteIfDone(ctx context.Context, jobID string) error

	// DispatchState says what this Controller's own record holds about
	// one device of one job: whether a Runner was handed its dispatch.
	// The run journal's consumer asks it before storing a batch a Runner
	// published, since any Runner may publish on any job's subject.
	DispatchState(ctx context.Context, jobID, deviceID string) (DispatchState, error)
}

// DispatchState is what a job's record says about one device's dispatch.
type DispatchState int

// The three answers DispatchState gives.
const (
	// DispatchUnrecorded means there is no such job, or no row for the
	// device yet. The second is ordinary for a moment: an unwindowed
	// fan-out publishes a dispatch before it records it.
	DispatchUnrecorded DispatchState = iota
	// DispatchSent means the device was handed to a Runner.
	DispatchSent
	// DispatchNotSent means the device was skipped, failed at dispatch,
	// or is still waiting in a forks window: no Runner has it.
	DispatchNotSent
)

// ClaimResult is what ClaimQueued found.
type ClaimResult int

// The three things ClaimQueued can find.
const (
	// ClaimMade means the task is now dispatched and holds the slot.
	ClaimMade ClaimResult = iota
	// ClaimSlotTaken means another device already holds the slot.
	ClaimSlotTaken
	// ClaimGone means the task is no longer queued, or the job is no
	// longer running, so there is nothing to claim.
	ClaimGone
)

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

// ErrCanceled is returned by RecordTask when jobID names a real job whose
// state is "canceled": somebody stopped it while this fan-out was in
// flight. Like ErrFenced it means stop and do not retry, and for the same
// reason, that retrying cannot make it untrue. It is a separate sentinel
// because the two say opposite things about whether the work should ever
// happen: a fenced worker was superseded and another worker is doing the
// job right now, while a canceled job is one nobody should carry on
// dispatching. A caller that collapsed them would log the wrong cause for
// whichever of the two it did not name. Callers should use errors.Is.
var ErrCanceled = errors.New("job was canceled")

// ErrNotCancelable is returned by Cancel when jobID names a real job that
// has already reached a terminal state. Stopping a job that has already
// stopped is not a failure of the platform and not a success either, so it
// is reported rather than silently treated as a no-op: an operator who
// pressed cancel deserves to be told the run had already finished, instead
// of being shown a success that implies they stopped something.
var ErrNotCancelable = errors.New("job is no longer running")
