package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Announcement holds the schema definition for the Announcement entity: a
// message an administrator puts in front of operators on the dashboard.
//
// It exists because a control plane that runs automation against production
// needs somewhere to say "change freeze until Monday" or "the east region is
// degraded, do not dispatch" to the people about to dispatch something. Out
// of band -- a chat channel, an email -- that message reaches whoever
// happened to be reading; here it reaches whoever is about to act.
//
// It is deliberately not a notification. internal/announce is a broadcast a
// human writes and every reader sees; Phase 28's Notification Engine is a
// per-event delivery to a configured target. Conflating them would mean one
// mechanism with two audiences and two lifetimes.
type Announcement struct {
	ent.Schema
}

// Mixin of the Announcement.
func (Announcement) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Announcement.
func (Announcement) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").NotEmpty(),
		field.Text("body").NotEmpty(),

		// level drives how prominently the message renders, and reuses the
		// vocabulary the UI's status badges already use rather than
		// inventing a second severity scale. A plain string rather than an
		// ent enum, matching Device.state's own reasoning: a new level can
		// be added without a migration, and an unrecognized value is
		// surfaced rather than coerced into one that means something else.
		field.String("level").Default("info").NotEmpty(),

		// The active window. Both optional, and they mean different things:
		// no start is "already showing", no end is "until somebody takes it
		// down". An announcement that has to be manually retired is how a
		// stale change-freeze banner ends up being ignored for a month, so
		// ends_at exists to make expiry the default rather than the
		// exception.
		field.Time("starts_at").Optional().Nillable(),
		field.Time("ends_at").Optional().Nillable(),

		// Immutable, and captured rather than joined. The author is a
		// historical fact about who said this; resolving it live would make
		// a deleted account silently blank the attribution on a message
		// people are still being asked to act on. Job.actor is stored the
		// same way for the same reason.
		field.String("author").NotEmpty().Immutable(),
	}
}

// Edges of the Announcement.
func (Announcement) Edges() []ent.Edge {
	return []ent.Edge{
		// Optional, and the absence is meaningful: an announcement with no
		// organization is system-wide and shown to everybody, which is what
		// a platform-level maintenance notice needs to be. One with an
		// organization is shown only to readers who can resolve that
		// organization, so a tenant's own change freeze does not leak the
		// tenant's plans to every other tenant.
		edge.From("organization", Organization.Type).
			Ref("announcements").
			Unique(),
	}
}

// Indexes of the Announcement.
func (Announcement) Indexes() []ent.Index {
	return []ent.Index{
		// Every read is "what is live right now", so the window is indexed
		// rather than the primary key: the dashboard runs this query on
		// every page load, for every reader.
		//
		// Liveness is evaluated against the server's clock at read time
		// with no grace period either side, deliberately. A tolerance would
		// mean a change freeze that has visibly ended still being displayed,
		// which is worse than one that disappears the moment it expires.
		index.Fields("starts_at", "ends_at"),
	}
}
