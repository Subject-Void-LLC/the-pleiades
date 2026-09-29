package sdk

import "fmt"

// The instruction a run emits describing how to undo itself.
//
// # Why this is emitted rather than declared
//
// A method's manifest answers only whether it CAN produce an inverse
// (pkg/collection.Reversibility). What the inverse actually is gets
// decided here, per run, because it depends on what the run found rather
// than on what the method is.
//
// The examples that force this are not edge cases. Starting a service
// that was already running must undo to nothing, while starting one that
// was stopped undoes to a stop, and those are the same method with the
// same parameters against two devices. Setting a file's mode undoes to
// whichever mode happened to be there. Removing a file is reversible
// only when the content was captured. A static declaration has to
// collapse all of that into one worst case, and a rollback engine then
// has to reconstruct the real instruction from recorded state, which
// means the reconstruction logic lives in the engine and has to know
// something about every method in the catalog.
//
// Emitting it moves that knowledge to the one place that has it. What
// lands in the journal is already parameterized and directly runnable,
// so a rollback engine executes tasks rather than interpreting them.
//
// # The shape
//
// An Inverse is a task. That is the whole design: undoing a run is
// running more tasks, through the same dispatcher, with the same
// capability checks, the same transport selection and the same audit
// trail. A rollback needs no second execution path.
//
// The run journal keeps the undo's method and parameter names, and the
// values of the parameters its method declares recordable (InverseSpec).
// `pleiades rollback` replays what the journal kept. Only the forward run
// can capture the values an undo needs, and once the change is applied
// they are gone.

// StatInverse is the stat key the emitted inverse is written under.
const StatInverse = "inverse"

// The keys of the record RecordInverse writes under StatInverse, for a
// reader that decodes it (the run journal's projection).
const (
	// InverseFQCNKey holds the undo's method.
	InverseFQCNKey = "fqcn"
	// InverseParamsKey holds the undo's parameters.
	InverseParamsKey = "params"
	// InversePartialKey is present, and true, when the undo is partial.
	InversePartialKey = "partial"
)

// Inverse is one concrete, already-parameterized instruction that undoes
// what a run did.
//
// It is deliberately the same shape as a runbook task, so a rollback
// engine dispatches it exactly as it would any other work.
type Inverse struct {
	// FQCN is the method that undoes this one. It is frequently the same
	// method that emitted it, with different parameters, which is the
	// common case rather than a special one: restoring a mode, a symlink
	// target or a file's content is the same operation as setting it.
	FQCN string `json:"fqcn"`

	// Params are the exact parameters to invoke FQCN with, already
	// resolved from what the run observed. Nothing here is a template and
	// nothing is left for a caller to fill in.
	Params map[string]any `json:"params"`

	// Description is one human-readable sentence saying what running this
	// would do, for an operator reading a rollback plan before approving
	// it. A plan of bare FQCNs and parameter maps is a plan nobody can
	// sanity check.
	Description string `json:"description,omitempty"`

	// Partial says running this would not put back everything the run
	// overwrote: file.copy's undo restores an overwritten file's mode and
	// owner but not its content, and a removed user comes back without its
	// password. A rollback replays a partial undo only when its operator
	// names it, since the device would be left different from before. A
	// method may set it only for a target its InverseSpec marks
	// MayBePartial.
	Partial bool `json:"partial,omitempty"`
}

// InverseSpec declares one method a Collection method's undo may call, in
// the method's manifest (collection.Reversibility.Inverses).
//
// It is an allowlist for the run journal and nothing more. The run decides
// whether there is an undo and what it says (RecordInverse); this decides
// which of that undo's parameters are identifiers safe to keep the values
// of, so a rollback can replay it. Anything else, the undo's other
// parameters or an undo naming a method not listed, is journaled by name
// only, and a rollback refuses to replay what it cannot see whole.
type InverseSpec struct {
	// FQCN is the method the undo calls.
	FQCN string `json:"fqcn"`

	// Record names the parameters whose values the journal may keep: a
	// name, a path, an id, a mode, a version, a number, a boolean. Never a
	// secret, never content read off the device, and never free text the
	// device holds, since the journal is plain JSON anyone who can read the
	// run's records can read.
	Record []string `json:"record,omitempty"`

	// Withhold names the parameters the undo may carry whose values are
	// never journaled: the prior content of a file, a mount's options (a
	// CIFS password can live there), a user's GECOS comment. An undo that
	// carries one cannot be replayed from the journal; validation says so
	// for a runbook that asks to be reversible.
	Withhold []string `json:"withhold,omitempty"`

	// MayBePartial says an undo calling FQCN may set Inverse.Partial.
	MayBePartial bool `json:"mayBePartial,omitempty"`
}

// RecordInverse writes inv as this task's undo instruction.
//
// A method calls it only when there is really something to undo. A run
// that found the device already converged must NOT call it, and that
// absence is meaningful rather than lazy: it is how the journal says
// "this task changed nothing, so undoing it means doing nothing." An
// inverse emitted for a run that changed nothing would make a rollback
// perform work the forward run never did.
func RecordInverse(rc RunbookContext, inv Inverse) error {
	if inv.FQCN == "" {
		// A caller that got this wrong would write an instruction naming no
		// method, which a rollback engine could only skip or fail on. Both
		// are worse than refusing here, where the method that produced it is
		// still on the stack.
		return fmt.Errorf("sdk: an inverse must name the method that undoes the run")
	}

	params := inv.Params
	if params == nil {
		// An empty map rather than nil, so a reader decoding the journal
		// finds an object where the contract says one is. A method whose
		// inverse genuinely takes no parameters is unusual but legal.
		params = map[string]any{}
	}

	record := map[string]any{
		InverseFQCNKey:   inv.FQCN,
		InverseParamsKey: params,
	}
	if inv.Description != "" {
		record["description"] = inv.Description
	}
	if inv.Partial {
		record[InversePartialKey] = true
	}
	return rc.SetStat(StatInverse, record)
}
