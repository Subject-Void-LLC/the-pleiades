package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"

	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SavedLaunchConfig holds the schema definition for a stored bundle of
// launch-time overrides: the values a particular way of running a template
// was saved with.
//
// PLAN.md Section 28 names it as what schedules and workflow nodes attach
// to. Neither exists yet, so it ships with the one consumer that does:
// relaunch. A job records the configuration it ran with, and relaunching
// reuses it rather than asking somebody to remember what they typed. That
// matters because the alternative to shipping it reachable is shipping it
// unreachable, which this repository has recorded three times.
//
// It is deliberately not the template. The spec's own word for this bundle
// is "LaunchConfig", and an earlier view took that name for the saved
// definition itself: it named the thing after its overrides and then
// described the definition. A template is what to run; this is one saved
// answer to how.
type SavedLaunchConfig struct {
	ent.Schema
}

// Mixin of the SavedLaunchConfig.
func (SavedLaunchConfig) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the SavedLaunchConfig.
func (SavedLaunchConfig) Fields() []ent.Field {
	return []ent.Field{
		// name is what a reader picks it by. Empty for the anonymous
		// configuration a relaunch stores against one job, which nobody
		// chooses from a list.
		field.String("name").Optional(),

		// fields are the launch overrides, keyed by launch field name, in
		// the same sparse shape internal/launch.Fields has: a key that is
		// absent means "not supplied" and inherits the template's, which is
		// a different instruction from a key present with an empty value.
		//
		// Sparseness has to survive storage, which is why this is a JSON
		// object rather than a row of columns with zero values. A column
		// set to its zero value cannot say whether anybody asked for it.
		field.JSON("fields", map[string]any{}).Optional(),

		// answers are the survey answers this configuration carries.
		//
		// Encrypted at rest through the same envelope encryption Device
		// properties use, by a hook registered in the composition root, and
		// the whole map is encrypted rather than only the password-typed
		// entries. Encrypting selectively would mean the hook had to know
		// which questions are passwords, which lives on the template's
		// survey and is exactly the kind of cross-entity knowledge a
		// mutation hook cannot reliably have. Encrypting everything is
		// strictly safer, costs nothing anybody needs (no query filters on
		// an answer value), and cannot be got wrong per row.
		//
		// Redaction on the way out is a separate concern with a separate
		// mechanism: the API projects a password answer as a marker rather
		// than its value, driven by the survey's own declared secret
		// variables. At rest and on the wire are two different exposures
		// and one control does not cover both.
		field.JSON("answers", map[string]any{}).Optional(),

		// secret_binding is the associated data that binds this row's
		// encrypted answers to this row.
		//
		// internal/crypto/envelope_bound.go closed this gap for Credential
		// in Phase 22 and recorded SavedLaunchConfig.answers as a known residual, with the
		// reason it was deferred: migrating it needs a rotation pass over
		// live encrypted data, which is its own piece of work with its own
		// failure modes. Phase 78c is that work.
		//
		// Two differences from Credential.secret_binding, both forced.
		//
		// Optional, because this column arrives on a table that already has
		// rows. A migration cannot generate a UUID per row portably (SQLite
		// has no function for it), so an existing row carries an empty
		// binding until something writes it. RotateDeviceProperties assigns
		// one as it converts, and the write hook assigns one to any row it
		// touches first.
		//
		// NOT Immutable, for the same reason. Credential's is immutable so
		// it cannot be edited to match a ciphertext somebody wants to
		// relocate, and that protection is worth having; here the column
		// has to be settable exactly once, on a row that has none, or no
		// existing row could ever be migrated. The application only ever
		// sets it when it is empty. The residual is the same class
		// Credential's own comment already records: a direct SQL writer
		// bypasses every rule this schema expresses.
		field.String("secret_binding").
			Optional().
			DefaultFunc(uuid.NewString),
	}
}

// Edges of the SavedLaunchConfig.
func (SavedLaunchConfig) Edges() []ent.Edge {
	return []ent.Edge{
		// A configuration belongs to exactly one template. Required,
		// because the fields it holds are only meaningful against that
		// template's declared prompts: the same map applied to a different
		// template would be a set of values nobody opened.
		edge.From("template", Template.Type).
			Ref("saved_configs").
			Unique().
			Required(),
	}
}

// Indexes of the SavedLaunchConfig.
func (SavedLaunchConfig) Indexes() []ent.Index {
	return []ent.Index{
		// Every read is "this template's saved configurations".
		index.Fields("name").Edges("template"),
	}
}
