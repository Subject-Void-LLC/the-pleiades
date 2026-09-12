// Package schema: the run journal's table (Phase 40).
//
// One row per node execution on the Walk tier, written by the Controller
// from what a Runner published. The type's own doc comment below carries
// the two things a reader needs before touching it: why this entity
// registers no crypto hook, and what identifies a row.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// JournalEntry holds the schema definition for one node execution's
// durable record: the Walk tier's landing place for the run journal
// internal/engine produces and internal/adapters/native publishes.
//
// # Why no crypto hook, stated rather than left absent
//
// Every other entity carrying anything sensitive registers an envelope
// encryption hook. This one deliberately registers none, and the absence
// is a decision rather than an oversight, so it is written here where
// the next person to add a field will read it.
//
// A journal entry holds no value that came back from a device, a
// credential store, a decrypted envelope, or an injector. It holds
// platform-generated identifiers, names resolved through the collection
// registry at write time, closed enums read off the executor's own
// control flow, a content digest, labels a runbook author wrote, and
// counts. internal/engine enforces that with two architecture tests that
// refuse any field able to carry a value, so there is nothing here to
// encrypt.
//
// TestEveryCryptoHookIsComposed cannot notice this either way: it fires
// on an exported hook that was written and never registered, not on an
// entity that has none. A field added here that broke the rule above
// would therefore be caught by internal/archtest's journal rules, not by
// the crypto gate.
//
// # Identity
//
// A row is identified by the dispatch, the device, the delivery attempt
// and the graph node: those four together are unique. RunID is not part
// of it, because the engine mints a fresh RunID on every Run call, so a
// redelivered dispatch re-running the same node produces a new one and
// would defeat the constraint it is supposed to satisfy.
type JournalEntry struct {
	ent.Schema
}

// Mixin of the JournalEntry.
func (JournalEntry) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the JournalEntry.
//
// The names are internal/engine's own json tags, so the column a reader
// queries and the key the Runner published carry the same name and no
// mapping table has to be kept in step.
func (JournalEntry) Fields() []ent.Field {
	return []ent.Field{
		// job_id is the dispatch this execution belongs to, captured as a
		// value rather than linked by edge, for the reason JobTask.device_id
		// already records: a job's history stays readable even after the
		// thing it names is gone. It is also the highest-write entity in
		// the system and retention for it is an open question, so a
		// foreign key would decide that question by accident.
		field.String("job_id").Immutable().NotEmpty(),
		// device_id is the device this node ran against. Optional because
		// a node can execute without resolving to any device at all: the
		// synthetic marker a parallel block produces is one.
		field.String("device_id").Immutable().Optional(),
		// attempt is JetStream's own redelivery counter for the dispatch,
		// so a second run against one device reads as a retry rather than
		// as two unrelated runs.
		field.Int("attempt").Immutable().NonNegative(),
		// node_id is the synthesized graph id, for example "tasks[0]",
		// never the task's register name.
		field.String("node_id").Immutable().NotEmpty(),

		// run_id identifies the one Executor.Run call this entry came
		// from. It does not identify the row (see the type's own note),
		// but it is what groups a retry's entries together.
		field.String("run_id").Immutable().NotEmpty(),
		// sequence orders entries inside one run. created_at cannot do
		// that job: a level fans out concurrently, so two nodes can carry
		// the same instant.
		field.Int("sequence").Immutable().NonNegative(),

		// dag_id is the runbook's author-written id, and dag_version is
		// the compiled definition's content hash. The second detects
		// drift between the runbook that ran and the runbook on disk now;
		// neither can recover that runbook's content, because nothing in
		// this platform stores one.
		field.String("dag_id").Immutable().Optional(),
		field.String("dag_version").Immutable().Optional(),

		// fqcn is the method this node ran, resolved through the registry
		// at write time rather than copied from what an author typed.
		// fqcn_unresolved reports that resolution found nothing, in which
		// case fqcn holds the engine's sentinel and the author's own
		// bytes were never stored.
		field.String("fqcn").Immutable().Optional(),
		field.Bool("fqcn_unresolved").Immutable().Default(false),

		// task_name and register are author-written labels, carried
		// through unchanged. They are the one residual channel by which a
		// human's own words reach this table, which is why the honest
		// guarantee is "no value the platform obtained" rather than "no
		// secret a human could type".
		field.String("task_name").Immutable().Optional(),
		field.String("register").Immutable().Optional(),

		// started_at and finished_at bound the execution itself, as
		// distinct from created_at, which records when the row was
		// written. A synthetic node that executed nothing carries the
		// zero value for both.
		field.Time("started_at").Immutable().Optional(),
		field.Time("finished_at").Immutable().Optional(),

		// outcome is internal/engine's closed enum, spelled here with the
		// identical values so the column stores what the engine produced
		// with no translation between them to drift.
		field.Enum("outcome").
			Values("ran", "changed", "skipped", "failed", "not_reached").
			Immutable(),
		// failure_stage and skip_kind are closed enums too, but both
		// include the empty string as a real member meaning "none", which
		// an ent enum cannot express. They are stored as their literal
		// engine values rather than mapped onto a "none" member, because
		// a mapping is one more thing that can drift from the vocabulary
		// it is mapping.
		field.String("failure_stage").Immutable().Optional(),
		field.String("skip_kind").Immutable().Optional(),
		// skip_ordinal and skip_total say which condition of how many
		// decided a skip, as numbers, so nothing has to parse English
		// back out of a reason sentence the author wrote.
		field.Int("skip_ordinal").Immutable().NonNegative().Default(0),
		field.Int("skip_total").Immutable().NonNegative().Default(0),

		// The key vectors: names only, never values. Each is paired with
		// a count of what the whitelist refused, because 38 of the 43
		// reversible methods declare no inverse in their Doc and three
		// declare no returns at all, so a Doc-derived whitelist has to
		// count what it rejects rather than drop it silently.
		field.Strings("stat_keys").Immutable().Optional(),
		field.Int("undeclared_stat_count").Immutable().NonNegative().Default(0),
		field.Strings("param_keys").Immutable().Optional(),
		field.Int("undeclared_param_count").Immutable().NonNegative().Default(0),

		// inverse_fqcn names what would undo this node, resolved through
		// the registry exactly as fqcn is. inverse_param_keys names the
		// parameters such a call takes without carrying any of their
		// values, so nothing here can run a rollback: only a later phase
		// that decides to store values could.
		field.String("inverse_fqcn").Immutable().Optional(),
		field.Bool("inverse_fqcn_unresolved").Immutable().Default(false),
		field.Strings("inverse_param_keys").Immutable().Optional(),
		field.Int("undeclared_inverse_param_count").Immutable().NonNegative().Default(0),

		// diff_recorded reports that the run captured a before and after,
		// without any of either. It is a decision the platform made, not
		// a value it observed.
		field.Bool("diff_recorded").Immutable().Default(false),
	}
}

// Indexes of the JournalEntry.
func (JournalEntry) Indexes() []ent.Index {
	return []ent.Index{
		// The identity constraint. A redelivered dispatch that re-runs a
		// node writes a row with a new attempt, so a retry is recorded
		// rather than refused; a duplicate publish of the same attempt is
		// refused rather than recorded twice. Without this, a Nak storm
		// produces uncorrelatable copies of one job that nothing can
		// collapse.
		index.Fields("job_id", "device_id", "attempt", "node_id").Unique(),
		// "this job's journal, in order" is the one query an operator
		// actually runs, and sequence alone is not enough to answer it
		// because two devices in one job each number from one.
		index.Fields("job_id", "device_id", "attempt", "sequence"),
	}
}
