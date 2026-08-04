package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// TimestampMixin adds created_at and updated_at to every schema that embeds
// it, so Memento point-in-time queries and retention jobs have one uniform
// pair of column names to order and expire rows by across every table.
//
// This is hand written rather than ent's builtin mixin.Time, which names
// its fields create_time/update_time. This codebase's own convention is
// the "_at" suffix (Revision.changed_at, the file repository's own
// changed_at sidecar field), so a borrowed mixin with different naming
// would make the schema internally inconsistent.
type TimestampMixin struct {
	mixin.Schema
}

// Fields of the TimestampMixin.
func (TimestampMixin) Fields() []ent.Field {
	return []ent.Field{
		// created_at is set once, at insert time, and never changes.
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		// updated_at is reset to now on every update. UpdateDefault, not
		// Default, is what makes it refresh on Update calls; Default alone
		// only fires at creation.
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}
