// Package schema's SyncRun: one attempt to fetch a project's source.
//
// The Project row carries only the LATEST outcome, because that is what a
// badge and a playbook lookup need. A history needs every attempt, and the
// two answer different questions: "is this project usable right now" against
// "why has it been failing since Tuesday". Keeping the latest on the project
// rather than deriving it from the newest run is deliberate, for the reason
// the Project schema's own comment gives about recording rather than
// rediscovering: a lookup that had to sort a history to learn the current
// revision would be a join on the hot path.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SyncRun holds the schema definition for one attempt to fetch a project's
// source.
//
// # A row now exists from the moment the attempt is claimed
//
// This schema used to record terminal outcomes only, on the argument that a
// running row duplicates the project's own sync_status and strands a row
// whenever a process dies mid-clone. Both halves of that argument were
// true and it was still the wrong trade, because an attempt with no row
// until it ends has no identity while it runs, and something has to be able
// to name the attempt it started: a schedule records which run its
// occurrence fired, and AWX's project_update, the object this is parity
// with, exists from the moment it is queued.
//
// So a row is created when Store.BeginSync claims the project, carrying
// status running and no finish time, and the same row is updated in place
// when the attempt ends. The stranding is handled where it always was:
// internal/project's startup recovery moves a running project to failed,
// and now fails its running row in the same sweep.
type SyncRun struct {
	ent.Schema
}

// Mixin of the SyncRun.
func (SyncRun) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the SyncRun.
func (SyncRun) Fields() []ent.Field {
	return []ent.Field{
		// running means the attempt is claimed and in flight on some
		// replica; the other two are terminal.
		field.Enum("status").
			Values("running", "succeeded", "failed"),

		// Who asked for this attempt: an identity's subject for a person,
		// or schedule.ScheduleActor's "scheduler:<id>" when a schedule
		// fired it. It is the audit trail's answer to "why did this clone
		// happen at 03:00", and it is taken from the request's identity,
		// never from a submission, for the reason every actor field in this
		// schema is.
		//
		// Optional because a row written before this field existed has
		// nobody recorded, which reads as empty rather than as a false
		// attribution.
		field.String("actor").
			Optional().
			MaxLen(255),

		// The commit this attempt ended at, empty for one that failed.
		field.String("revision").
			Default("").
			MaxLen(64),

		// Why this attempt failed. Held to the same rule the Project's own
		// sync_error is: internal/project strips a URL's userinfo before an
		// error reaches here, because this is shown to an operator.
		field.String("error").
			Default("").
			MaxLen(2048),

		// When the clone began and when it ended. Both are recorded rather
		// than one plus a duration, because "it started at 03:00 and is
		// still the last thing that ran" is the question a history is opened
		// to answer, and a duration alone cannot answer it.
		//
		// finished_at is empty for exactly as long as the attempt is
		// running. Nillable rather than a zero time, so that "not finished"
		// is a state the column itself can hold instead of a magic
		// timestamp every reader has to know about.
		field.Time("started_at"),
		field.Time("finished_at").
			Optional().
			Nillable(),
	}
}

// Edges of the SyncRun.
func (SyncRun) Edges() []ent.Edge {
	return []ent.Edge{
		// A run belongs to exactly one project and is meaningless without
		// it, so the edge is required; deleting the project takes its
		// history with it, which is the same lifetime the checkout has. The
		// cascade that makes that true is declared on the Project side,
		// which owns the edge.
		edge.From("project", Project.Type).
			Ref("sync_runs").
			Unique().
			Required(),
	}
}

// Indexes of the SyncRun.
func (SyncRun) Indexes() []ent.Index {
	return []ent.Index{
		// The one access pattern: this project's attempts, newest first.
		index.Fields("started_at").
			Edges("project"),
	}
}
