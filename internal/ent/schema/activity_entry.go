package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ActivityEntry holds the schema definition for one line of the activity
// stream: who changed which managed object, and when.
//
// It exists because the platform already decides this and then throws it
// away. auth.Recorder writes every admission decision to log/slog, where no
// operator can read it and no auditor can query it, and nothing at all
// records the administrative writes themselves. "Who added that user to the
// admin team last Thursday" was, until this table, a question with no
// answer anywhere in the system.
//
// It is deliberately not Revision, which is already in this schema and
// looks superficially similar. Revision is a per-device property trail:
// field_name, old_value, new_value, hung off one Device by a required edge,
// answering "what changed on this device". This answers "who changed which
// object", across every managed object kind, including the ones that have
// no properties at all. Merging them would mean either a device trail with
// three quarters of its columns null or an activity stream that can only
// describe devices.
type ActivityEntry struct {
	ent.Schema
}

// Mixin of the ActivityEntry.
//
// created_at is the entry's time and nothing ever updates a row here, so
// updated_at will always equal it. The mixin is applied anyway, for the
// reason Revision states: uniform column names are what let a retention job
// expire every table the same way, and an audit table is the one most
// likely to be handed to a retention policy.
func (ActivityEntry) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the ActivityEntry.
func (ActivityEntry) Fields() []ent.Field {
	return []ent.Field{
		// actor is the authenticated subject that made the change, captured
		// at write time and immutable. Announcement.author is stored the
		// same way for the same reason: this is a historical fact about who
		// acted, and resolving it live would let deleting an account silently
		// blank the attribution on everything that account ever did, which
		// is the one edit an attacker would most want to make.
		field.String("actor").NotEmpty().Immutable(),

		// action is what happened, from a closed vocabulary the decorator
		// owns. Unlike Announcement.level, an unrecognized value here is not
		// something to render neutrally and move on from: the only writer is
		// internal/access's audited store, so a value outside the set means
		// the writer has a bug, not that a newer version knows a word this
		// one does not.
		field.String("action").NotEmpty().Immutable(),

		// object_kind and object_id name what was acted on. Deliberately a
		// kind string plus a bare integer rather than an edge, for two
		// reasons that both matter.
		//
		// The first is polymorphism: one stream covers organizations, teams,
		// users, role bindings and contacts today, and every object kind a
		// later phase adds. Five nullable edges would become fifteen.
		//
		// The second is the one that decides it. An edge would carry a
		// foreign key, and a foreign key means the deletion of an object
		// either cascades away its own audit trail or is blocked by it.
		// "Who deleted this and when" is precisely the entry an auditor
		// needs after the object is gone, so the audit trail must outlive
		// what it describes.
		field.String("object_kind").NotEmpty().Immutable(),
		field.Int("object_id").Immutable(),

		// object_name is the object's name as it was at the moment of the
		// change, captured rather than joined.
		//
		// This is the opposite of how a list renders a reference, and the
		// difference is deliberate. A Teams list resolves its organization's
		// name live, because it is describing what is true now and a rename
		// should show through. An activity entry describes what was true
		// then: showing today's name against a change made under the old one
		// would misreport history, and a deleted object would leave the line
		// describing nothing at all.
		field.String("object_name").Optional().Immutable(),
	}
}

// Indexes of the ActivityEntry.
func (ActivityEntry) Indexes() []ent.Index {
	return []ent.Index{
		// The stream is read newest first and paged by id, which the primary
		// key already serves. These cover the two narrowing questions an
		// investigation actually opens with: everything one subject did, and
		// everything that happened to one object.
		index.Fields("actor"),
		index.Fields("object_kind", "object_id"),
	}
}
