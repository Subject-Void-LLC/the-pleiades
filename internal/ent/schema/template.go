package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Template holds the schema definition for a launch template: the saved,
// reusable definition of something this platform can run.
//
// It exists because the launch surface was four scalars. Dispatching meant
// naming a group and a runbook in a query string, with no way to save the
// pairing, no way to vary it deliberately, and no record of the decisions
// somebody made about how to run it. AWX calls this a Job Template and
// Semaphore a Task Template; the sentence both build around, and the one
// this schema's three load-bearing parts map onto, is that a template
// defines what to run (kind and definition), where to run it (the inventory
// edge) and how to run it (defaults and prompts).
type Template struct {
	ent.Schema
}

// Mixin of the Template.
func (Template) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Template.
func (Template) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("description").Optional(),

		// kind is the registry key of what this runs, and it is a plain
		// string rather than an ent enum. That is the single most important
		// decision in this file and it is not a shortcut.
		//
		// An ent enum becomes a closed set in the database: a CHECK
		// constraint in Postgres, a rewritten table in SQLite. Registering
		// a new launch kind would then require a schema migration in every
		// deployment before anybody could use it, which is exactly the
		// closed-vocabulary cost PLAN.md Section 28's open registry exists
		// to avoid. Device.state and Announcement.level are plain strings
		// for a weaker version of the same reason.
		//
		// The vocabulary is still closed at the write: internal/launch
		// refuses a template whose kind names no registered descriptor. The
		// difference is that the set lives in one Go registry rather than
		// in the registry and in two dialects' DDL.
		field.String("kind").NotEmpty(),

		// definition is the reference the kind resolves: a runbook id, a
		// playbook path. Immutable, because re-pointing a template at
		// different code while keeping its name, its access grants and its
		// job history is how a reviewed thing quietly becomes an unreviewed
		// one. Editing what runs means making a new template, which leaves
		// two legible records instead of one silent change.
		field.String("definition").NotEmpty().Immutable(),

		// defaults is how to run it: the field values this template was
		// saved with, keyed by launch field name.
		//
		// JSON rather than a column per field, and that follows from the
		// open kind exactly as internal/launch.Fields does. A kind arrives
		// in a package this schema has never seen, bringing fields nothing
		// here declared, so a column per field cannot be written. JSON
		// stays queryable in Postgres, which a bytes column would not.
		field.JSON("defaults", map[string]any{}).Optional(),

		// prompts names the fields a launch may override. Everything else
		// is locked to what defaults says.
		//
		// A list of names rather than AWX's seventeen parallel
		// ask_*_on_launch boolean columns, forced by the same open kind: a
		// boolean per field cannot be declared for fields nothing here
		// knows about.
		field.JSON("prompts", []string{}).Optional(),

		// required_caps is what a device must be able to do for this to
		// run, recorded when the template is saved rather than computed on
		// demand.
		//
		// Recorded because computing it means compiling the definition,
		// which the write path can do once and a launch would otherwise do
		// on every dispatch. The staleness that buys is real and bounded: a
		// runbook edited after a template was saved leaves this behind,
		// which is why it is a plan-time hint and the executor still
		// acquires capabilities for real at run time.
		field.JSON("required_caps", []string{}).Optional(),

		// survey_enabled is separate from having no questions, deliberately.
		// A template author who has written a survey and turned it off has
		// said something different from one who has not written a survey,
		// and deleting the questions to disable it would lose the work.
		field.Bool("survey_enabled").Default(false),

		// allow_simultaneous permits more than one job from this template to
		// run at once. Semaphore calls it "allow parallel tasks" and
		// defaults it off, which is the right default here too: two runs of
		// the same change against the same fleet is more often a mistake
		// than an intention.
		field.Bool("allow_simultaneous").Default(false),
	}
}

// Edges of the Template.
func (Template) Edges() []ent.Edge {
	return []ent.Edge{
		// The tenancy boundary. Required, and it is what makes a job
		// launched from this template belong to somebody: Job's own
		// organization_id has had no writer since it was added, because
		// nothing upstream of a dispatch carried a tenant.
		edge.From("organization", Organization.Type).
			Ref("templates").
			Unique().
			Required(),

		// Where it runs. Required, and it is the other half of the same
		// sentence: an Inventory carries a required organization edge, so a
		// template naming one has a tenant by construction rather than by
		// somebody remembering to set it.
		//
		// The store refuses a template whose inventory belongs to a
		// different organization than the template's own. ent cannot
		// express that, and getting it wrong is not a tidiness problem: it
		// would let a template in one tenant dispatch against another
		// tenant's hosts, which is FAILURE_PATTERNS.md #97's shape with
		// every individual step passing its own check.
		edge.From("inventory", Inventory.Type).
			Ref("templates").
			Unique().
			Required(),

		// The survey's questions, ordered. Cascade is declared on this,
		// the owning side, because a question belongs to exactly one
		// template and outliving it would leave a question nobody can
		// answer attached to nothing.
		edge.To("survey_questions", SurveyQuestion.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),

		// Saved launch configurations, same ownership and same cascade.
		edge.To("saved_configs", SavedLaunchConfig.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),

		// The credentials this template runs as.
		//
		// Many to many. ent cascades the JOIN rows on both sides, which is
		// exactly right and worth stating precisely, because "cascade" on
		// a table holding secrets is the kind of word that gets misread:
		// deleting a template removes its BINDINGS, not the credentials
		// they point at, and deleting a credential removes its bindings,
		// not the templates. A credential outlives every template that
		// binds it. Nothing here can destroy secret material as a side
		// effect of deleting something else.
		//
		// This is the axis AWX has and this platform did not. Before it,
		// authentication was resolved per device from a file-backed store
		// keyed by device name, which cannot express the case the parity
		// corpus shows plainly: one job template binding an ssh, a vault
		// and an aws credential at once. Both axes now exist, and the
		// precedence between them is stated at the one place they meet
		// (internal/dispatch's fan-out): a machine credential bound here
		// supplies auth for every device in the fan-out, AWX's own
		// semantics, and the per-device store is the fallback consulted
		// only when a template binds none. That keeps every Crawl-tier
		// dispatch and every pre-existing Walk dispatch working unchanged.
		edge.To("credentials", Credential.Type),

		// The schedules that launch this template.
		//
		// Deliberately NOT cascaded, unlike survey_questions and
		// saved_configs above, and the difference is the point: those two
		// are parts of the template and meaningless without it, whereas a
		// schedule is an independent object an operator created and can
		// see in its own list. Deleting a template out from under a
		// schedule should be refused, not silently take the schedule with
		// it -- the deletion is the moment to tell somebody that automation
		// they rely on is about to stop.
		edge.To("schedules", Schedule.Type),
	}
}

// Indexes of the Template.
func (Template) Indexes() []ent.Index {
	return []ent.Index{
		// Name is unique within an organization, not globally, matching
		// Inventory's own constraint. Two tenants both having a "patch the
		// edge routers" template is the ordinary case.
		index.Fields("name").
			Edges("organization").
			Unique(),

		// "which templates run this runbook" is the question asked when a
		// runbook is about to change, and it reads across organizations.
		index.Fields("kind", "definition"),
	}
}
