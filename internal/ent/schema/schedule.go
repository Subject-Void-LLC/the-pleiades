package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// newScheduleID generates the stable opaque identifier a new Schedule row
// gets by default, following job.go's newJobID exactly: UUIDv7 so the
// column is time-ordered and usable as a keyset tiebreaker, falling back to
// v4 if the entropy source fails, since a non-monotonic opaque id is still
// a valid one.
func newScheduleID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.New().String()
}

// Schedule holds the schema definition for a recurrence attached to
// something launchable: when automation runs without somebody pressing
// launch.
//
// It attaches to a Launchable rather than to a Template, and that
// distinction is the whole of Phase 21's C1 seam. A Launchable row stands
// for one thing that can be run, whatever sort of thing it is, so this one
// edge reaches a job template and a project sync alike, and will reach a
// workflow when there is one. PLAN.md Section 30.1 states the requirement as
// "RFC5545 recurrence rules attached to any Launchable", and this is what
// "any" actually costs.
//
// It used to point at Template, on the argument that a Template carries its
// own kind so one edge covered every kind. That was true and answered the
// wrong question: a kind is which ENGINE runs a definition (runbook or
// playbook), while what a schedule needs to name is which OBJECT to run, and
// a project sync is not a Template of any kind
// (.SPECIFICATION/AWX_PARITY_ROADMAP.md section 1.1).
//
// The recurrence itself is three columns rather than one, and the split is
// deliberate. AWX stores a single rrule blob with DTSTART and TZID folded
// inside it, which means answering "what time zone is this schedule in"
// requires parsing the rule. Keeping timezone and dtstart as their own
// columns makes them queryable, indexable and editable in a form, and
// makes the rrule column exactly one thing: a recurrence.
type Schedule struct {
	ent.Schema
}

// Mixin of the Schedule.
func (Schedule) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Schedule.
func (Schedule) Fields() []ent.Field {
	return []ent.Field{
		// schedule_id is the stable opaque identifier callers reference,
		// distinct from the internal auto-increment primary key for the
		// same reason Job.job_id is.
		field.String("schedule_id").
			Immutable().
			Unique().
			NotEmpty().
			DefaultFunc(newScheduleID),

		field.String("name").NotEmpty(),
		field.String("description").Optional(),

		// enabled is how a schedule is turned off without being destroyed.
		// Deleting would lose the occurrence history that hangs off it,
		// which is the record of what this schedule did while it was
		// running -- the same distinction Survey.Enabled draws.
		field.Bool("enabled").Default(true),

		// rrule is the RFC 5545 recurrence, validated by
		// internal/schedule/rrule at save time against that package's
		// constraint set. A rule that cannot be expanded safely never
		// reaches this column.
		field.String("rrule").NotEmpty(),

		// exclusions are EXRULE and EXDATE lines subtracted from rrule's
		// occurrences. JSON rather than a joined table because they are
		// only ever read as a set, together with the rule they modify, and
		// never queried individually.
		field.JSON("exclusions", []string{}).Optional(),

		// timezone is an IANA zone name, validated against the generated
		// allowlist in internal/schedule/zoneinfo at save time.
		//
		// It is carried beside the rule rather than inside it because it
		// is the field that decides what the rule MEANS: "daily at 09:00"
		// in America/New_York preserves the wall-clock hour across a
		// daylight saving transition, so two consecutive runs can be 23 or
		// 25 hours apart. Storing it separately is what lets a form show
		// it and a query group by it.
		field.String("timezone").NotEmpty().Default("UTC"),

		// dtstart is the recurrence anchor, stored in UTC.
		//
		// RFC 5545 takes from DTSTART every field the rule leaves
		// unspecified -- the time of day always, and the day or month for
		// the coarser frequencies -- so it is part of the recurrence's
		// meaning, not merely the moment it was created.
		field.Time("dtstart"),

		// dtend bounds the schedule from the outside. Optional: absent
		// means open-ended, matching AWX's own null dtend.
		//
		// It is separate from the rule's own UNTIL because the two are
		// different authorities. UNTIL is part of the recurrence an author
		// wrote; dtend is an operator saying "stop running this after
		// then" without editing the rule.
		field.Time("dtend").Optional().Nillable(),

		// next_run is the next occurrence at or after now, in UTC, and it
		// is a MATERIALISED CACHE rather than the source of truth. The
		// rrule is the truth; this is recomputed from it on every write
		// and after every fire.
		//
		// AWX exposes next_run as a computed field, and tests/parity
		// records it that way, so storing it looks like a divergence and
		// is worth explaining rather than leaving to be rediscovered: a
		// keyset-paginated due-scan cannot index a value that only exists
		// in a response body. PLAN.md Section 30.1 requires the scan to be
		// keyset-paginated ("a growing pending set with offset paging
		// degrades quadratically"), and that requires an indexed column.
		// It stays nillable so "no further occurrences" is representable
		// as absence rather than as a sentinel date somebody has to know.
		field.Time("next_run").Optional().Nillable(),

		// last_fired is the occurrence time of the most recent firing, in
		// UTC, and it is what the coalescing missed-run policy measures
		// from. It is the OCCURRENCE's time, not the moment the job was
		// created: after an outage those differ, and measuring from the
		// wrong one would either replay history or skip it.
		field.Time("last_fired").Optional().Nillable(),
	}
}

// Edges of the Schedule.
func (Schedule) Edges() []ent.Edge {
	return []ent.Edge{
		// The tenancy boundary, required, matching Template's own. A
		// schedule is authorised by the organization it belongs to, and
		// the store refuses one whose template belongs to a different
		// organization -- ent cannot express that, and getting it wrong
		// would let one tenant's schedule launch another tenant's work.
		edge.From("organization", Organization.Type).
			Ref("schedules").
			Unique().
			Required(),

		// What it launches. Required and unique: a schedule with nothing to
		// run is not a schedule.
		//
		// The key is NO ACTION, deliberately uncascaded, and it is what
		// protects a schedule from its target disappearing: deleting a
		// template or a project cascades into its launchable row, this key
		// refuses that while a schedule still points at it, and the delete
		// fails as a whole. A deletion is the moment to tell somebody that
		// automation they rely on is about to stop, rather than silently
		// taking the schedule with it.
		edge.From("launchable", Launchable.Type).
			Ref("schedules").
			Unique().
			Required(),

		// How it launches: the saved bundle of launch-time overrides this
		// schedule runs with, the same entity a relaunch already reuses.
		// Optional -- a schedule with none runs the template's own
		// defaults. This is AWX's schedule.extra_data, which
		// tests/parity/fields_related.go already classifies as Convertible
		// onto exactly this entity.
		edge.To("saved_config", SavedLaunchConfig.Type).
			Unique(),

		// What it has done. The audit trail of every occurrence, fired or
		// skipped.
		edge.To("occurrences", ScheduleOccurrence.Type),
	}
}

// Indexes of the Schedule.
func (Schedule) Indexes() []ent.Index {
	return []ent.Index{
		// Name is unique within an organization, not globally, matching
		// Template's own constraint: two tenants both having a "nightly
		// backup" schedule is the ordinary case.
		index.Fields("name").
			Edges("organization").
			Unique(),

		// The due-scan index, and the reason next_run is a stored column.
		//
		// The scan is "enabled schedules whose next_run has passed, in
		// next_run order, resuming after a cursor", so the column order
		// here is the query's own: equality on enabled, range on next_run,
		// then schedule_id as the tiebreaker that makes the keyset cursor
		// total. Without the tiebreaker, two schedules sharing a next_run
		// could straddle a page boundary and one of them be skipped.
		index.Fields("enabled", "next_run", "schedule_id"),
	}
}
