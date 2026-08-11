package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Session holds the schema definition for the Session entity: one
// browser's authenticated session with the web UI.
//
// It exists because a browser cannot present a Bearer token the way an API
// or CLI client can. An EventSource cannot set an Authorization header at
// all, which is why the SSE job-log stream was unreachable from the SPA
// this replaced, and a token held in JavaScript is a token exposed to
// every script on the page. A cookie the server issues and can revoke
// solves both.
//
// .SPECIFICATION/PATTERNS.md's "Stateless Session" entry rejects a
// server-side session store, on the grounds that it is "stateful
// infrastructure that needs replicating and invalidating across every
// Controller Pod". That argument is correct about one thing and not the
// other, and the distinction is what this schema turns on. Node-local
// session state -- an in-process map -- does force Sticky Session, and is
// still rejected; nothing here keeps a session in memory. A row in the
// same PostgreSQL every pod already holds an ent.Client against forces
// nothing: any pod resolves any session with zero coordination, and the
// row *is* the invalidation. The cost is one indexed primary-key read per
// request, and what it buys is the thing a signed JWT structurally cannot
// give: revocation. A browser credential that survives a logout is a
// materially worse posture for an air-gapped, high-security target than
// that read.
//
// The Bearer path is untouched. This adds a cookie mode for browsers; it
// does not replace token authentication for API, CLI, or MCP clients.
type Session struct {
	ent.Schema
}

// Mixin of the Session.
func (Session) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Session.
func (Session) Fields() []ent.Field {
	return []ent.Field{
		// token_hash is the SHA-256 of the session identifier, never the
		// identifier itself.
		//
		// The cookie a browser holds is the only place the real value
		// exists. A database dump, a replica, a backup, or a support
		// engineer reading rows therefore yields no usable credential --
		// hashing is what makes stolen storage useless rather than
		// equivalent to stolen sessions. This is the same posture
		// internal/crypto's envelope encryption already takes for
		// credential material, applied to the one other secret this
		// schema would otherwise store in the clear.
		field.Bytes("token_hash").
			Unique().
			Immutable().
			MaxLen(32).
			NotEmpty(),

		// subject, role and scopes are the auth.Identity this session
		// resolves to, captured once at login.
		//
		// They are stored rather than re-derived because the token that
		// proved this identity is not kept: the session is the record of
		// what was proven, and re-deriving it would mean keeping the
		// credential around to re-check, which is exactly what hashing
		// the identifier above exists to avoid.
		field.String("subject").NotEmpty().Immutable(),
		field.String("role").Immutable(),
		field.JSON("scopes", []string{}).Optional().Immutable(),

		// csrf_key is the per-session HMAC key this session's CSRF tokens
		// are derived from.
		//
		// Per-session and server-held is what closes the hole in naive
		// double-submit CSRF. With a shared or client-derivable key, an
		// attacker who can set a cookie on the origin can also forge a
		// matching token; with a key that never leaves the server and
		// differs per session, they cannot. Rotating the session rotates
		// the key for free, so there is no second rotation schedule to
		// forget.
		field.Bytes("csrf_key").
			Immutable().
			MaxLen(32).
			NotEmpty().
			Sensitive(),

		// idle_expires_at and absolute_expires_at are two independent
		// deadlines, and both are needed.
		//
		// Idle expiry closes an abandoned session on a shared machine; it
		// slides forward as the session is used. Absolute expiry bounds
		// the total life of a credential no matter how continuously it is
		// exercised, so an attacker who steals a cookie cannot keep it
		// alive indefinitely simply by using it -- which is precisely
		// what an idle timeout alone would permit.
		field.Time("idle_expires_at"),
		field.Time("absolute_expires_at").Immutable(),

		// last_seen_at records the most recent use, for operators
		// answering "is anyone actually using this session" without
		// having to infer it from the sliding idle deadline.
		field.Time("last_seen_at").Default(time.Now),
	}
}

// Indexes of the Session.
func (Session) Indexes() []ent.Index {
	return []ent.Index{
		// Every authenticated request resolves a session by hash, so this
		// is the hot path and the reason the column is unique-indexed
		// rather than merely unique.
		index.Fields("token_hash"),
		// The expiry sweeper scans on the absolute deadline. Without this
		// index, the periodic cleanup would table-scan the busiest table
		// in the schema.
		index.Fields("absolute_expires_at"),
	}
}
