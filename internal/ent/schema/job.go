package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

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
		//
		// SUPERSEDED by inventory_id below, as of Phase 21. A launch now
		// names a template, and a template names an inventory; there is no
		// remaining path that targets a bare group name. The column stays,
		// written empty, rather than being dropped, and that is a decision
		// rather than an oversight: FAILURE_PATTERNS.md #59 records that
		// this repository's migration generator silently never emits DROP
		// COLUMN, so removing a NOT NULL column means a hand-written
		// migration in two dialects, which is its own change with its own
		// test rather than a side effect of this one.
		field.String("group_name").Immutable(),

		// inventory_id is what the dispatch actually targets: the shareable
		// device set the template names. The worker streams its membership,
		// which is its groups plus the devices attached to it directly.
		//
		// Denormalized rather than an edge, for the reason organization_id
		// gives below and JobTask.device_name gives for itself: a job is a
		// historical record. An edge would mean deleting an inventory
		// either cascades away every job that ever ran against it or is
		// blocked by them, and "what did this dispatch target" is precisely
		// the question somebody has after the inventory is gone.
		//
		// Optional and Nillable because the column has to tolerate a job
		// created by a path that names no inventory. Every path that exists
		// today writes it.
		field.Int("inventory_id").Optional().Nillable().Immutable(),

		// template_id and template_name record which saved definition this
		// job came from, captured at launch.
		//
		// The name is captured beside the id for the reason
		// ActivityEntry.object_name is: a template's job history has to
		// outlive the template, and a job row that resolved the name live
		// would describe nothing once somebody deleted it. The id is kept
		// too, so a template's own Completed Jobs section is one indexed
		// query rather than a scan.
		field.Int("template_id").Optional().Nillable().Immutable(),
		field.String("template_name").Optional().Immutable(),

		// launch_config_id names the SavedLaunchConfig this job was
		// launched with: the bundle of overrides and survey answers the
		// caller supplied, stored before the job so the job can point at
		// it.
		//
		// It is what makes relaunch mean what its name says. Without it a
		// relaunch could only re-run the template as saved, which for any
		// job launched with overrides would quietly run something other
		// than the job it claims to be repeating -- and for a template with
		// a required survey question, something that would not run at all.
		//
		// Nil for a launch that supplied nothing, which is not the same as
		// "supplied an empty configuration": there is no row because there
		// was nothing to record, and a relaunch of such a job correctly
		// re-runs the template's own defaults.
		//
		// Denormalized rather than an edge, for the reason template_id and
		// organization_id give above: a job is a historical record, and an
		// edge would make deleting a template either cascade away the jobs
		// it launched or be blocked by them. A dangling id here is the
		// honest outcome, and Relaunch reports the configuration as gone
		// rather than silently launching without it.
		field.Int("launch_config_id").Optional().Nillable().Immutable(),

		// kind is the launch kind this job ran as: which registered
		// descriptor, and therefore which execution adapter, handled it.
		//
		// Recorded so a job record says what it was without joining back to
		// a template that may since have been deleted, and so a jobs list
		// can show whether a run was native or sandboxed. A plain string
		// rather than an ent enum, for the reason Template.kind gives: the
		// kind vocabulary is an open registry, and an enum would put a
		// closed copy of it in two dialects' DDL.
		field.String("kind").Optional().Immutable(),
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
		// "canceled" and "running" were added together by Item I (job
		// cancel). "canceled" is a person's decision to stop a run and is
		// terminal: a job reaches it only from "pending" or "fanning_out",
		// through JobStore.Cancel's own compare-and-swap, and nothing moves
		// it out again. It is its own state rather than "failed" carrying a
		// reason, because an operator scanning a job list needs to tell a
		// run somebody stopped from one that broke on its own, and because
		// AWX, which this platform targets parity with, carries the same
		// distinction.
		//
		// "running" is declared here but nothing writes it yet, and that is
		// deliberate rather than an oversight. "completed" today means the
		// fan-out finished, NOT that the devices finished: no consumer folds
		// per-device execution results back onto this row, so there is no
		// moment at which this Controller could honestly say a job is still
		// running. Declaring the value now costs nothing (neither dialect
		// constrains this column, so widening the enum is a Go-side change
		// with no migration) and saves widening it a second time when that
		// consumer is built.
		field.Enum("state").
			Values("pending", "fanning_out", "running", "completed", "failed", "canceled").
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
		// canceled_at and canceled_by record who stopped this job and when.
		// Both are empty for every state other than "canceled".
		//
		// Separate from failure_reason rather than folded into it: that
		// column answers "why could the worker not proceed", which is a
		// fact about the platform, and these answer "who decided to stop
		// this", which is a fact about a person. An audit trail that
		// conflated the two would make a deliberate stop indistinguishable
		// from a fault at exactly the moment somebody is asking which it
		// was.
		//
		// canceled_by holds the actor's subject, the same value actor above
		// carries for whoever launched the job. The two differ often: a
		// scheduled job is launched by the scheduler and stopped by a
		// person, and that difference is the point of recording it.
		field.Time("canceled_at").Optional(),
		field.String("canceled_by").Optional(),
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

		// fields is the resolved launch.Resolved.Fields this job was
		// dispatched with: limit, verbosity, forks, timeout, and whichever
		// kind-specific fields (job_tags, skip_tags) its kind declares.
		// Stamped once at creation and never changes, matching runbook_id's
		// own "a record of what was requested" reasoning immediately above.
		//
		// Captured on the job record before it reaches the wire or either
		// adapter: internal/adapters/legacy's argv construction and
		// internal/adapters/native's extra-variable injection are a
		// separate, not-yet-built consumer of this same data
		// (AWX_PARITY_ROADMAP.md's launch-fields-reach-execution phase).
		// Recording it here first is what makes the job record honest about
		// what a launch was configured with even before that phase lands,
		// the same incremental widening this schema's own comments describe
		// for organization_id and failure_reason.
		field.JSON("fields", map[string]any{}).Optional().Immutable(),

		// extra_vars is the resolved launch.Resolved.ExtraVars this job was
		// dispatched with: the template's defaults, a saved configuration,
		// survey answers, and this launch's own overrides, already merged
		// in that precedence order by launch.Template.Resolve. Same
		// capture-now, consume-later status as fields above.
		field.JSON("extra_vars", map[string]any{}).Optional().Immutable(),

		// credential_ids are the credentials this job's template was bound
		// to at the moment it was launched, in binding order.
		//
		// It is the audit answer to "what did this run authenticate as",
		// and it is recorded here rather than joined back to the template
		// for the reason template_name beside it is: a job's history has to
		// outlive the definition it came from, and a template's bindings can
		// be changed by anybody holding credential:write after the job ran.
		// Joining would report what the template says today, which is not
		// what the run used.
		//
		// Ids and nothing else. No name, no type, and above all no value:
		// the values are resolved at fan-out and never touch this table
		// (see internal/dispatch's own fan-out injection for why injection
		// happens there rather than at launch). An operator wanting to know
		// what credential 7 was reads credential 7.
		//
		// A relaunch deliberately does NOT read this column. It re-reads the
		// template's current bindings, because a credential rotated or
		// rebound since the original run is what an operator relaunching
		// expects to pick up.
		field.JSON("credential_ids", []int{}).Optional().Immutable(),
	}
}

// Edges of the Job.
func (Job) Edges() []ent.Edge {
	return []ent.Edge{
		// A job has many per-device task outcomes, its fan-out record.
		edge.To("tasks", JobTask.Type),
	}
}

// Indexes of the Job.
func (Job) Indexes() []ent.Index {
	return []ent.Index{
		// "what has this template run" is a template detail page's own
		// Completed Jobs section, and it is the only cross-job query this
		// phase adds. Indexed rather than scanned, because the jobs table
		// is the one that grows fastest here.
		index.Fields("template_id"),
	}
}
