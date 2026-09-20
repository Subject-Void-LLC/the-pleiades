// Package schema's Launchable: one row per thing this platform can be asked
// to run, whatever sort of thing it is.
//
// This is AWX's UnifiedJobTemplate expressed in a relational schema rather
// than in class inheritance. Its purpose is that a schedule (and later a
// workflow node, and a notification policy) holds ONE foreign key, into one
// table, and therefore needs to know nothing about which sort of object it
// points at. A schedule attached to a job template and one attached to a
// project sync are the same row shape and travel the same code path.
//
// # Why the pointers live here rather than on each target
//
// Each target type gets one nullable, unique column on THIS table, and a
// CHECK requires exactly one of them to be set. The alternative, a required
// pointer on templates and projects into this table, was rejected on three
// edge cases: a crash between the two inserts leaves an orphan row here that
// no constraint can forbid; every target's store then has to delete its own
// row here, so a type that forgets leaves a schedule pointing at nothing;
// and in SQLite it means rebuilding every target table instead of creating
// one new one.
//
// This direction gets the database to do the work instead. The pointer
// cascades, so deleting a template deletes its launchable row; a schedule's
// own foreign key into this table is NO ACTION, so that cascade is refused
// while a schedule still points at it, and the whole delete fails as one
// statement. No target store has to know consumers exist, and a future
// consumer protects every target type by declaring one NO ACTION key.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Launchable holds the schema definition for the launchable base row.
type Launchable struct {
	ent.Schema
}

// Mixin of the Launchable.
func (Launchable) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Launchable.
func (Launchable) Fields() []ent.Field {
	return []ent.Field{
		// The registered launchable type: internal/launchable's key, which is
		// AWX's own subclass name ("job_template", "project").
		//
		// A plain string rather than an ent enum, for the reason
		// Template.kind is one: the vocabulary is an OPEN registry, and an
		// enum column would mean a migration for every type added, which is
		// the closed-set cost the registry exists to avoid. Immutable
		// because a row is one object's identity and that object does not
		// change sort.
		field.String("type").
			NotEmpty().
			Immutable().
			MaxLen(64),

		// The target's own name, copied here when the target is written.
		//
		// Denormalized deliberately, which is how AWX's base table works
		// too. Everything that reads launchables reads them to render a
		// picker or a list, and resolving the name per type would mean a
		// query per type and a branch on which. The target's store keeps it
		// in step inside the same transaction that renames the target.
		field.String("name").
			NotEmpty().
			MaxLen(253),
	}
}

// Edges of the Launchable.
func (Launchable) Edges() []ent.Edge {
	return []ent.Edge{
		// The tenant. Required, and the reason a consumer can check tenancy
		// without knowing the type: a schedule compares its own organization
		// to this one.
		edge.From("organization", Organization.Type).
			Ref("launchables").
			Unique().
			Required(),

		// Exactly one of these points at the object this row stands for. Both
		// are optional in the schema because only one is ever set; the CHECK
		// below is what makes "exactly one" true, and the target's own store
		// is what writes it.
		edge.From("template", Template.Type).
			Ref("launchable").
			Unique(),

		edge.From("project", Project.Type).
			Ref("launchable").
			Unique(),

		// What points at this launchable. A schedule's key is NO ACTION, so
		// a launchable a schedule still uses cannot be deleted, and neither
		// can the target whose delete would cascade into it.
		edge.To("schedules", Schedule.Type),
	}
}

// Indexes of the Launchable.
func (Launchable) Indexes() []ent.Index {
	return []ent.Index{
		// The one listing: this organization's launchables, of one type or
		// all of them, in name order. It is what a picker reads.
		index.Fields("type", "name").
			Edges("organization"),
	}
}

// Annotations of the Launchable.
//
// The CHECK is what makes this table a discriminated union rather than a row
// that might point at two objects or at none. It is written with CASE rather
// than num_nonnulls, which Postgres has and SQLite does not, so one
// expression serves both dialects.
func (Launchable) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Checks(map[string]string{
			"launchable_exactly_one_target": "((CASE WHEN template_launchable IS NULL THEN 0 ELSE 1 END) + " +
				"(CASE WHEN project_launchable IS NULL THEN 0 ELSE 1 END)) = 1",
		}),
	}
}
