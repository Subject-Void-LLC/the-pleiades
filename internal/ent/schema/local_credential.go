package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// LocalCredential holds the schema definition for the LocalCredential
// entity: one User's locally stored password, per PLAN.md Section 18.1's
// "Hashed passwords stored in the Postgres DB".
//
// # Why this is not a column on User
//
// The obvious shape is password_hash on User, and it is the wrong one for a
// reason that has nothing to do with schema taste. ent.User is loaded and
// projected onto access.User, then onto the API's user DTO, then onto the
// users list view. A hash column on User puts a hash field on every one of
// those structs, and the only thing keeping it out of an HTTP response is
// somebody remembering. internal/credstore's package doc rejects exactly
// that arrangement: a rule of the form "handlers must remember to redact"
// holds until the day somebody adds a handler, and the failure is silent
// and permanent. A separate entity means the projections that render a page
// or answer a request load a row that has no hash in it at all.
//
// .Sensitive() below is necessary and not sufficient. It omits the field
// from the generated String() and from JSON marshaling, and leaves it fully
// readable from Go. The package boundary in internal/localauth is what
// actually does the work; this is the second layer, not the only one.
//
// # Why this does not contradict User's own Edges() comment
//
// internal/ent/schema/user.go records that a "role" string field was
// REMOVED from User because per-user grants are the orphaned-permission
// anti-pattern PLAN.md Section 18.2 forbids: "Roles are assigned to Teams,
// never directly to Users." That comment reads as "nothing belongs on
// User", and the distinction matters here. A role is a GRANT, and a grant
// on a User survives the removal of that User from the Team that was
// supposed to bound it. A password is not a grant. It is proof of who the
// subject is, which is the one thing a User already claims to be, and it
// carries no permission at all: what a locally authenticated caller may do
// is still resolved entirely from the RoleBindings on their Teams. Deleting
// this row revokes nothing and grants nothing; it only stops the person
// proving they are that subject with a password.
type LocalCredential struct {
	ent.Schema
}

// Mixin of the LocalCredential.
func (LocalCredential) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the LocalCredential.
func (LocalCredential) Fields() []ent.Field {
	return []ent.Field{
		// password_hash is a PHC-encoded Argon2id digest, never a
		// reversible encryption of the password.
		//
		// The whole string is stored rather than a bare digest beside
		// parallel cost columns, because rehash-on-login needs the
		// parameters that produced THIS hash and one self-describing
		// string means they arrive with the row already loaded: there is
		// no second column to forget to write and no way for the two to
		// disagree. The algorithm prefix is also what lets a later cost
		// bump land with no migration at all, the same argument PLAN.md
		// Section 17.2 already makes for the "v1$AES256GCM$" ciphertext
		// prefix.
		//
		// internal/crypto is deliberately not used here and is the wrong
		// tool: it is reversible by design, its Decrypt is public, and one
		// process-wide KEK covers every row, so a single key compromise
		// would expose every account at once. internal/archtest enforces
		// that internal/localauth never depends on it.
		field.String("password_hash").
			NotEmpty().
			Sensitive(),

		// failed_attempts and locked_until are per-account lockout state,
		// and they live in the shared database rather than in a process.
		//
		// internal/ent/schema/session.go's own header already made this
		// argument for sessions and it transfers without modification: an
		// in-process counter is node-local state, so with N replicas an
		// attacker gets N times the threshold before any single replica
		// reacts, and a restart silently resets it to zero. Both failures
		// are silent, which is the worst property a lockout can have.
		//
		// The increment must be a single atomic statement (ent's
		// AddFailedAttempts), never a read-modify-write in Go, or a
		// parallel burst all reads the same starting value.
		field.Int("failed_attempts").
			Default(0).
			NonNegative(),

		// Nillable because "not locked" and "locked until the zero time"
		// are different facts, and a zero time compares as long past,
		// which would read as an expired lock rather than as no lock.
		//
		// A lock is ALWAYS time bounded. A permanent lock on the
		// break-glass account PLAN.md Section 18.1 exists to harden is a
		// remote off switch an unauthenticated attacker can throw.
		field.Time("locked_until").
			Optional().
			Nillable(),

		// password_changed_at is what a "your password is N days old"
		// prompt or an access review reads. Password expiry itself is out
		// of scope for the phase that adds this entity; recording the fact
		// is not, because the fact cannot be reconstructed afterwards.
		field.Time("password_changed_at").
			Default(time.Now),

		// must_change marks a credential written by an administrator
		// rather than chosen by its owner, so a bootstrap or reset
		// password can be required to be replaced at first use instead of
		// silently becoming permanent.
		field.Bool("must_change").
			Default(false),
	}
}

// Edges of the LocalCredential.
func (LocalCredential) Edges() []ent.Edge {
	return []ent.Edge{
		// A LocalCredential MUST belong to exactly one User, and a User
		// has at most one. User owns this edge; this is the Ref side.
		//
		// Required means the row cannot exist detached from the identity
		// it proves, so there is no way to end up with a password whose
		// owner was deleted.
		edge.From("user", User.Type).
			Ref("local_credential").
			Unique().
			Required(),
	}
}
