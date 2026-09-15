// Package schema's SyncRun: one completed attempt to fetch a project's
// source.
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

// SyncRun holds the schema definition for one completed attempt to fetch a
// project's source.
//
// # Only terminal outcomes are recorded
//
// A row is written when an attempt finishes, so status is succeeded or
// failed and never running. An attempt still in flight is visible on the
// project's own sync_status, which is what the page's badge reads; writing a
// running row here too would put the same fact in two places and leave a
// stranded one behind whenever a process died mid-clone, which is precisely
// the state internal/project's startup recovery exists to clear.
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
		field.Enum("status").
			Values("succeeded", "failed"),

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
		field.Time("started_at"),
		field.Time("finished_at"),
	}
}

// Edges of the SyncRun.
func (SyncRun) Edges() []ent.Edge {
	return []ent.Edge{
		// A run belongs to exactly one project and is meaningless without
		// it, so the edge is required; deleting the project takes its
		// history with it, which is the same lifetime the checkout has.
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
