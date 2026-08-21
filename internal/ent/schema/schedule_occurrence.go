package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ScheduleOccurrence holds the schema definition for one occurrence of a
// Schedule: the immutable record of a moment the schedule came due and what
// happened at it.
//
// It is modelled on JobTask, which is the same shape for a different parent
// -- an immutable per-unit outcome row with an enum and a reason -- because
// the questions asked of it are the same: what did this do, and where it
// did nothing, why.
//
// Two distinct jobs are done by one table, and it is worth being explicit
// that this is deliberate rather than conflated:
//
//  1. It is the audit trail. A compliance reader asking "did the nightly
//     backup run every night last quarter" needs the nights it did NOT run
//     to be rows, not gaps. A missing row and a row saying "skipped,
//     missed_window" are the same absence of a job and completely
//     different answers, and only the second one is auditable.
//
//  2. It is the duplicate-fire guard. The unique index on (schedule,
//     occurrence_at) below is what actually makes a schedule fire once.
//     Leader election reduces contention but cannot guarantee singularity:
//     internal/election runs a two-second lease with a half-second poll and
//     exposes no fencing token, so during a failover two replicas can
//     briefly both believe they lead. The claim is inserted in the same
//     transaction that creates the Job and BEFORE anything is published,
//     so a second claimant loses on the constraint rather than on timing.
type ScheduleOccurrence struct {
	ent.Schema
}

// Mixin of the ScheduleOccurrence.
func (ScheduleOccurrence) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the ScheduleOccurrence.
func (ScheduleOccurrence) Fields() []ent.Field {
	return []ent.Field{
		// occurrence_at is the recurrence instant this row is about, in
		// UTC, and it is the identity of the occurrence rather than a
		// timestamp of when the row was written (TimestampMixin's
		// created_at is that, and after an outage the two differ by the
		// length of the outage).
		//
		// Storing UTC is what makes the unique index below meaningful: two
		// replicas in different process time zones must compute the same
		// key for the same occurrence, and a wall-clock value in a zone
		// with a daylight saving fold is not unique within its own day.
		field.Time("occurrence_at").Immutable(),

		// outcome is the occurrence's state, and it is the one field on
		// this row that is written twice rather than once.
		//
		// "claimed" is the initial value and exists to make the
		// duplicate-fire guard atomic. Inserting this row IS the claim:
		// the unique index below means the second of two contending
		// replicas fails the insert rather than racing on timing. Only
		// once a replica holds the claim does it launch, and only then
		// does it write the real outcome.
		//
		// "fired" means a job was created and published; job_id names it.
		// "skipped" means it was not, and reason says why.
		//
		// A row left at "claimed" is therefore meaningful rather than
		// corrupt: it says a controller won the claim and died before it
		// could record what happened. That is the residual risk of
		// creating a job and publishing it in two steps -- the same one
		// api.Dispatcher.LaunchTemplate already carries for a manual
		// launch -- and this makes it VISIBLE, where the manual path
		// leaves it silent. It is deliberately not swept automatically:
		// re-firing could double-run, and abandoning could lose a run, so
		// the choice belongs to an operator looking at the row.
		//
		// There is deliberately no "failed": a failure after the job
		// exists belongs to the Job, which already records outcomes per
		// device. Duplicating it here would give two answers to one
		// question and no rule for which wins.
		field.Enum("outcome").
			Values("claimed", "fired", "skipped").
			Default("claimed"),

		// reason explains a skipped outcome and is empty for a fired one.
		//
		// It names a policy, never data: "missed_window",
		// "missed_window_truncated", "schedule_disabled",
		// "template_unresolvable". Like JobTask.reason it is an audit
		// field, so it must never carry a value read out of a device
		// property or a survey answer -- those are envelope-encrypted at
		// rest and echoing one here would write a secret into the clear.
		field.String("reason").Optional(),

		// suppressed_count is how many further occurrences this one row
		// stands for, and it is non-zero only on a truncated skip.
		//
		// A one-minute schedule that missed a year is half a million
		// occurrences; writing a row each would turn a recovery into an
		// outage of its own. Beyond a cap the scanner writes one row
		// carrying the count instead. It is a real column rather than
		// text inside reason because "how many runs did we lose" is a
		// question somebody will want to sum, and because a silent cap
		// would make truncation indistinguishable from completeness.
		field.Int("suppressed_count").Default(0).Immutable(),

		// job_id is the Job this occurrence created, set only when the
		// outcome is fired. It is the opaque Job.job_id string rather than
		// an edge, matching how Job itself denormalises the things it
		// refers to: an occurrence is a historical record and must stay
		// readable after whatever it points at is gone.
		//
		// Written on the transition out of "claimed", not at insert: the
		// job does not exist yet at the moment the claim is taken, which
		// is precisely the ordering that makes the claim safe.
		field.String("job_id").Optional(),
	}
}

// Edges of the ScheduleOccurrence.
func (ScheduleOccurrence) Edges() []ent.Edge {
	return []ent.Edge{
		// An occurrence MUST belong to exactly one Schedule.
		edge.From("schedule", Schedule.Type).
			Ref("occurrences").
			Unique().
			Required(),
	}
}

// Indexes of the ScheduleOccurrence.
func (ScheduleOccurrence) Indexes() []ent.Index {
	return []ent.Index{
		// THE duplicate-fire guard. See the type's own doc comment for why
		// this, rather than leader election, is what makes a schedule fire
		// exactly once.
		//
		// ent emits the columns as (occurrence_at, schedule) whichever way
		// round this is written -- fields always precede edges in a
		// generated index -- so the two spellings are one index and one
		// name. Uniqueness is unaffected by column order, which is all this
		// entry is for; the history query needs the other order and gets
		// its own index below.
		index.Edges("schedule").
			Fields("occurrence_at").
			Unique(),

		// The read index: the foreign key on its own.
		//
		// The only query run against this table is "this schedule's
		// occurrences, most recent first", which filters on the schedule
		// and sorts on the time. The ideal index for it leads with the
		// schedule, and ent cannot express that -- a generated index always
		// orders declared fields before edges, so index.Edges("schedule").
		// Fields("occurrence_at") and its reverse both emit
		// (occurrence_at, schedule) and collide on one name. The v0.14.6
		// entsql annotations offer prefixes and operator classes but no
		// column reordering.
		//
		// Indexing the edge alone is what remains expressible, and it is
		// most of the benefit: it turns the filter into a range scan over
		// one schedule's rows, leaving only the sort, over a set already
		// bounded by that schedule. This table grows by one row per
		// occurrence per schedule forever, so leaving it to a full scan
		// would degrade steadily and invisibly.
		index.Edges("schedule"),
	}
}
