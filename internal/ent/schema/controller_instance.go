// Package schema's ControllerInstance: the fleet table, one row per running
// controller process, kept current by that process's own heartbeat.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ControllerInstance is one running controller process, as its own heartbeat
// last described it.
//
// It answers two questions nothing else could. An operator upgrading a fleet
// asks which builds are still running, and whether the rollout has finished,
// which controller migrate --plan reads from here. And a controller starting
// up has to tell a sync that a dead process abandoned from one a live peer is
// still running, which it used to answer by failing both (FAILURE_PATTERNS.md
// #278); a sync now records the instance that owns it, and only an owner
// missing from here is presumed dead.
//
// Liveness is measured on the DATABASE's clock, in whole Unix seconds: every
// write sets last_seen_unix from the server's own time, and every reader
// compares against that same clock, so two controllers whose clocks disagree
// cannot make each other look dead. It is an integer rather than a time
// because SQLite stores ent's times as text, and comparing those in SQL
// against the server's clock is exactly the kind of arithmetic that is right
// on one dialect and quietly wrong on the other.
//
// The rows are written by raw SQL (internal/ent/instances.go), not through
// the generated client, since an upsert stamped with the server's clock is
// not something the generated builders can express.
//
// This table is never contracted: a build before any change to it still
// heartbeats into it, so it may only ever gain nullable columns.
type ControllerInstance struct {
	ent.Schema
}

// Mixin gives the table the created_at/updated_at pair every table has. The
// heartbeat writes both from the controller's clock; neither is used to judge
// liveness.
func (ControllerInstance) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the ControllerInstance.
func (ControllerInstance) Fields() []ent.Field {
	return []ent.Field{
		// instance_id is random per process start, so a restarted controller
		// is a new instance and its predecessor's row ages out rather than
		// being mistaken for it.
		field.String("instance_id").NotEmpty().MaxLen(64).Immutable().Unique(),

		// version is the build's reported version (internal/buildinfo).
		field.String("version").MaxLen(128),

		// migration_head is the newest migration this build knows, which is
		// what decides whether it can serve a schema a newer build moved on.
		field.String("migration_head").MaxLen(128),

		// host names where the process runs: a pod or machine name, so an
		// operator can find it. Never an address a secret rides on.
		field.String("host").Default("").MaxLen(255),

		// started_unix and last_seen_unix are the database server's clock,
		// in seconds.
		field.Int64("started_unix").Immutable(),
		field.Int64("last_seen_unix"),
	}
}

// Indexes of the ControllerInstance.
func (ControllerInstance) Indexes() []ent.Index {
	return []ent.Index{
		// Every read is "who has been seen since then".
		index.Fields("last_seen_unix"),
	}
}
