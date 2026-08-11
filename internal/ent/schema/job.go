package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"

	"github.com/google/uuid"
)

// Job holds the schema definition for the Job entity: one asynchronous
// dispatch request against a runbook and a target group, and the record a
// GET on the job resource is served from while the fan-out to individual
// devices runs in the background.
type Job struct {
	ent.Schema
}

// Mixin of the Job.
func (Job) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// newJobID generates the stable opaque identifier a new Job row gets by
// default. UUIDv7 is time-ordered, so a future keyset-paginated listing of
// jobs can page on this column without a separate ordering key, mirroring
// device.go's newDeviceID; NewV7 only errors on an entropy-source failure,
// in which case a random v4 is still a valid, if non-monotonic, opaque
// identifier.
func newJobID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.New().String()
}

// Fields of the Job.
func (Job) Fields() []ent.Field {
	return []ent.Field{
		// job_id is the stable, opaque identifier callers poll on. It is
		// distinct from the internal auto-increment primary key (a
		// storage-layer detail used only for edges/FKs), so the wire
		// reference a client holds never breaks even if the row is moved
		// or the backend changes.
		field.String("job_id").
			Immutable().
			Unique().
			NotEmpty().
			DefaultFunc(newJobID),
		// runbook_id names the runbook this job dispatches. It is stamped
		// once at creation and never changes: a job is a record of what
		// was requested, not a mutable pointer that could later be
		// repointed at a different runbook.
		field.String("runbook_id").NotEmpty().Immutable(),
		// group_name restricts the dispatch to devices in the named group.
		// Deliberately not NotEmpty(): pkg/inventory/selector.go's
		// Selector.GroupName documents that an empty GroupName means no
		// restriction (dispatch to every device), which is a legitimate
		// value this schema must be able to store, not an omission to
		// reject.
		field.String("group_name").Immutable(),
		// actor is the identity subject that requested the job, stamped
		// once at creation so the audit trail can always answer "who asked
		// for this dispatch" without depending on a separate log surviving.
		field.String("actor").NotEmpty().Immutable(),
		// organization_id is the tenancy boundary this dispatch happened
		// inside, stamped once at launch and never resolved live.
		//
		// Denormalized on purpose, matching group_name and actor directly
		// above rather than being an edge. A job is a historical record,
		// and joining back through the group to whichever inventory holds
		// it today would let a later re-parenting silently rewrite which
		// tenant a past dispatch appears to belong to. The same reasoning
		// the JobTask schema gives for capturing DeviceName at dispatch
		// time rather than linking to a device that may since be renamed.
		//
		// Optional and Nillable because a dispatch against a group that
		// belongs to no inventory has no organization to stamp, which is
		// the ordinary case in a single-tenant deployment. Nil means
		// "unscoped", never "unknown".
		field.Int("organization_id").Optional().Nillable().Immutable(),
		// state is the job's lifecycle: "pending" (created, not yet picked
		// up), "fanning_out" (a worker is actively dispatching to devices),
		// "completed" (every device has been dispatched, skipped, or
		// failed and the terminal tallies below are final), or "failed"
		// (the worker could not even begin dispatching, e.g. runbook_id
		// names no runbook this Controller can resolve; see
		// failure_reason). A later worker's own idempotent-consumer guard
		// conditionally transitions pending to fanning_out exactly once
		// even under at-least-once redelivery, so two redelivered claims
		// of the same job can never both believe they own the fan-out.
		//
		// "failed" was added by internal/dispatch (Phase 14, The
		// Dispatcher worker) rather than reusing "completed" with
		// zero-valued tallies: a job that legitimately ran against an
		// empty group also completes with dispatched_count, skipped_count,
		// and failed_count all 0, so representing "the runbook could not
		// even be resolved, no device was ever considered" as
		// "completed" with the same three zeros would make the two
		// genuinely different outcomes indistinguishable to a caller
		// polling the job resource. "failed" plus failure_reason keeps
		// them distinguishable.
		field.Enum("state").
			Values("pending", "fanning_out", "completed", "failed").
			Default("pending"),
		// dispatched_count, skipped_count, and failed_count are the
		// terminal tallies a GET on this job resource reports. They start
		// at 0 and are set once, together, when the job transitions to
		// "completed"; until then they read as 0 regardless of how much
		// fan-out work has actually happened, since the job's own tasks
		// edge is the live-progress source of truth.
		field.Int("dispatched_count").Default(0),
		field.Int("skipped_count").Default(0),
		field.Int("failed_count").Default(0),
		// failure_reason explains a "failed" state: why the worker could
		// not begin (or finish) dispatching at all. It is empty for every
		// other state. Like JobTask.reason, it must only ever name the
		// runbook_id or a similarly non-sensitive fact, never echo a raw
		// internal error string that might carry a filesystem path or a
		// storage-layer detail a job-resource reader has no business
		// seeing.
		field.String("failure_reason").Optional(),
		// fence is the fan-out lease's fencing token: a monotonically
		// increasing counter bumped by exactly 1, atomically, every time
		// BeginFanOut successfully claims or reclaims ownership of this
		// job's fan-out (internal/dispatch/ent_store.go's BeginFanOut).
		// Every subsequent state-mutating call for that claim (RecordTask,
		// Complete, Fail) must present the fence value the caller obtained
		// from its own BeginFanOut call, and the storage layer conditions
		// its own write on fence still matching. This is what lets a
		// worker that has been superseded by a stale reclaim (its
		// heartbeat went quiet past fanOutLeaseTTL while it was still,
		// unbeknownst to it, alive and working) be rejected by the
		// database itself the next time it tries to write, rather than
		// merely trusted to notice on its own and stop; see
		// dispatch.ErrFenced. Starts at 0 for a freshly created,
		// never-claimed job, since fence only carries meaning once a
		// BeginFanOut call has bumped it at least once.
		field.Int64("fence").Default(0),
	}
}

// Edges of the Job.
func (Job) Edges() []ent.Edge {
	return []ent.Edge{
		// A job has many per-device task outcomes, its fan-out record.
		edge.To("tasks", JobTask.Type),
	}
}
