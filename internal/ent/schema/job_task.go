package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// JobTask holds the schema definition for one device's outcome within a
// Job's fan-out: it was dispatched to, skipped, or failed. A job with a
// forks window (Phase 110) first records an admitted device as waiting
// (see waiting below), and that row moves once, when the window has room
// for it; every other row is written once and never changes its outcome.
type JobTask struct {
	ent.Schema
}

// Mixin of the JobTask.
func (JobTask) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the JobTask.
func (JobTask) Fields() []ent.Field {
	return []ent.Field{
		// device_id is the target device's stable opaque identifier
		// (Device.device_id), captured at dispatch time rather than linked
		// by edge, so a job's history stays readable even if the device is
		// later renamed or removed from inventory.
		field.String("device_id").Immutable().NotEmpty(),
		// device_name is the target device's name at dispatch time,
		// captured alongside device_id for the same reason: a human
		// reading this job's history later should not need to resolve an
		// id back through inventory to know which device a row is about.
		field.String("device_name").Immutable().NotEmpty(),
		// outcome is one of "dispatched" (the runbook was successfully
		// handed off for this device), "skipped" (the device was
		// deliberately excluded, e.g. lifecycle state or a missing
		// capability), or "failed" (dispatch was attempted and did not
		// succeed). skipped always carries a non-empty reason; dispatched
		// typically carries an empty one.
		//
		// It is not Immutable, because a waiting row has to become
		// dispatched for real, skipped or failed when its turn comes, and a
		// dispatched row whose publish failed has to become failed. Those
		// moves are enforced by internal/dispatch's store, each one a
		// conditional update on the value it moves from. Immutable is Go
		// side only, so dropping it changes nothing a database holds.
		field.Enum("outcome").
			Values("dispatched", "skipped", "failed"),
		// reason explains a skipped or failed outcome. It must only ever
		// name a device (its Name), its lifecycle State, or a missing
		// capability.Name. It must NEVER contain a device's Properties()
		// value: those are envelope-encrypted secrets decrypted
		// transparently on every read via
		// crypto.DeviceEnvelopePropertiesInterceptor, and this field is
		// effectively an audit trail, so a reason string that echoed a
		// property value would leak a secret into it. Set when the outcome
		// is, and only then; not Immutable for the same reason outcome is.
		field.String("reason").Optional(),

		// result, result_reason and finished_at record what happened when
		// the runbook actually RAN on this device, which is a different
		// fact from outcome above and is why they are separate fields
		// rather than more values on that enum.
		//
		// outcome answers "did the fan-out hand this device off", is
		// decided by the Controller, and is immutable because it is
		// history the moment it is written. These answer "what did the
		// Runner make of it", are decided on the other side of the mesh
		// and arrive later, and so cannot be immutable. Folding the two
		// together would also destroy information: a device whose
		// dispatch succeeded and whose run then failed would become
		// indistinguishable from one that was never dispatched at all.
		//
		// All three are empty until a result arrives, and stay empty
		// forever for a device that was skipped or never dispatched to.
		// A job whose tasks all carry a result is one every device has
		// reported back on, which is what moves it out of "running".
		field.Enum("result").
			Values("succeeded", "failed").
			Optional(),
		// result_reason explains a failed result. It carries the same
		// obligation reason above does and for the identical reason: it
		// crosses the mesh from a Runner and lands in an audit trail, so
		// it must never echo a device property or a raw internal error.
		field.String("result_reason").Optional(),
		field.Time("finished_at").Optional(),
		// unchecked is how many tasks a check could not check on this
		// device, reported with the result, and zero for a real run and
		// for a check that answered for every task. A check job is
		// complete only when every device's count is zero.
		field.Int("unchecked").Default(0).NonNegative(),

		// The window's two columns come last, after every column a build
		// from before the window knows.
		//
		// waiting marks a device a windowed job admitted and has not yet
		// dispatched: it waits for a place in the job's forks window.
		// Its row reads outcome "dispatched" with waiting true, and
		// internal/dispatch reports it as queued.
		//
		// A flag beside outcome rather than a new outcome value, and that
		// is for a Controller from before the window, which may share the
		// database during a rolling upgrade. It would fail to read an
		// outcome it does not know, and worse, it counts only dispatched
		// rows with no result as work still out, so a new value would let
		// it complete a windowed job while devices still waited. Reading
		// "dispatched" instead, it holds the job open until every waiting
		// device has run, which is the right answer; it only shows them as
		// dispatched a little early. The column is NOT NULL with a default,
		// so the migration adding it only expands the schema
		// (internal/ent/migrate/compat.go).
		field.Bool("waiting").Default(false),

		// slot is the window position a windowed job's dispatched device
		// holds while it runs, 0 up to the job's forks less one, and nil
		// for every other row: unwindowed jobs, queued rows, and a device
		// whose result has come back, which frees it. The unique index on
		// (job, slot) below is what bounds a job to forks devices at once,
		// by constraint rather than by counting, so two Controller
		// replicas pumping one job cannot both take the last free place.
		// NULLs are distinct in a unique index on SQLite and PostgreSQL
		// alike, so any number of rows may hold no slot.
		field.Int("slot").Optional().Nillable().NonNegative(),
	}
}

// Edges of the JobTask.
func (JobTask) Edges() []ent.Edge {
	return []ent.Edge{
		// A JobTask MUST belong to exactly one Job.
		edge.From("job", Job.Type).
			Ref("tasks").
			Unique().
			Required(),
	}
}

// Indexes of the JobTask.
func (JobTask) Indexes() []ent.Index {
	return []ent.Index{
		// A job's own GET response needs "this job's tasks by outcome"
		// (the counts and the per-outcome lists it reports), so the pair
		// is indexed together rather than each column alone.
		index.Fields("outcome").Edges("job"),
		// At most one row per window slot per job: the forks bound. See
		// slot above.
		index.Fields("slot").Edges("job").Unique(),
	}
}
