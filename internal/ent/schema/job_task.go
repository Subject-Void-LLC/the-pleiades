package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// JobTask holds the schema definition for one device's outcome within a
// Job's fan-out. Each row is the immutable record of what happened when
// the dispatcher considered one device for one job: it was dispatched to,
// skipped, or failed.
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
		field.Enum("outcome").
			Values("dispatched", "skipped", "failed").
			Immutable(),
		// reason explains a skipped or failed outcome. It must only ever
		// name a device (its Name), its lifecycle State, or a missing
		// capability.Name. It must NEVER contain a device's Properties()
		// value: those are envelope-encrypted secrets decrypted
		// transparently on every read via
		// crypto.DeviceEnvelopePropertiesInterceptor, and this field is
		// effectively an audit trail, so a reason string that echoed a
		// property value would leak a secret into it.
		field.String("reason").Optional().Immutable(),

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
	}
}
