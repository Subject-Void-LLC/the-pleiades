// Package engine: the run journal's entry vocabulary and its sink port.
// JournalEntry is one node execution's durable record, Journal is where
// Executor.Run will write them, and the three enums here are the closed
// sets an entry may use to say what a run did.
//
// This file is the vocabulary and the seam. The projection that turns a
// NodeResult into a JournalEntry, and the Record call in Run's own level
// loop, live in journal_entry.go. An Executor built with no sink still
// gets the no-op default and behaves exactly as it did before either file
// existed.
package engine

import (
	"context"
	"encoding/json"
	"time"
)

// JournalEntry is one node execution's durable record.
//
// # The field names are the format
//
// Every field carries a snake_case json tag, and those tags are the
// on-disk names in the Crawl tier's JSON Lines file and the on-wire names
// in the Walk tier's published record. Changing one is a format change,
// not a rename, so it belongs in a commit that says so.
//
// They live on this type rather than on a separate wire struct in
// internal/journal on purpose. A wire struct would need a field-by-field
// mapping function, and the failure mode of such a function is that a
// field added here is silently absent there: the entry still marshals,
// the tests still pass, and the one run that needed the new field is the
// one that did not record it. Two sinks reading one tagged type cannot
// disagree about the format. The cost is that this package now names a
// serialization, which is stated here rather than left for a reader to
// discover.
//
// No field of this type carries omitempty, so every record carries every
// field. (InverseParam, the element type of InverseParams, does: a
// parameter holds one of text, number or bool.) A synthetic fan-out marker therefore writes an empty FQCN and a zero
// StartedAt and FinishedAt, which is the honest record of a node that
// executed nothing, not corruption.
//
// # The provenance rule
//
// A field belongs in this type only if it is one of seven kinds. This is
// the rule a reviewer applies to every field a later phase proposes, so
// it is written out here in full rather than left in a design note:
//
//  1. A platform-generated identifier: RunID, Sequence, NodeID (the
//     synthesized graph id, see NodeResult.NodeID), DeviceID (the
//     inventory item's own stored id field, internal/inventory/record/
//     record.go:161, never a device property), JobID, Attempt, StartedAt,
//     FinishedAt, and a rollback's RollbackOf and UndoesNode.
//  2. A compile-time constant resolved through a registry at write time:
//     FQCN and InverseFQCN after resolution through collection.Lookup,
//     the stat and param key names a method's own Doc declares, and the
//     two pkg/sdk stat keys (sdk.StatInverse, sdk.StatDiff).
//  3. A closed enum this package defines: Outcome, FailureStage,
//     SkipKind. A bool is the two-valued case of the same thing, and is
//     admitted here rather than as a kind of its own: FQCNUnresolved,
//     InverseFQCNUnresolved, DiffRecorded, InverseComplete,
//     InversePartial, ActionChanged and AuthoredRollback each report a
//     decision the platform made or a flag a method raised, never a value
//     it observed.
//  4. A content-addressed digest: DAGVersion, and nothing else.
//     DAG.Version is computed, never authored (dag.go:313-329), a
//     sha256:<hex> over the fully resolved definition.
//  5. An author-written label from the compiled runbook: DAGID, TaskName,
//     Register. See below.
//  6. A count: SkipOrdinal, SkipTotal, UndeclaredStatCount,
//     UndeclaredParamCount, UndeclaredInverseParamCount, UndoesStep.
//  7. An undo parameter's value that the emitting method declares an
//     identifier (sdk.InverseSpec.Record, in its manifest): InverseParams.
//     A name, a path, an id, a mode, a version, a number or a boolean the
//     run observed or the author gave, held to 256 bytes of valid,
//     terminal-safe UTF-8, and admitted only for a method compiled into
//     The Pleiades.
//
// Nothing admitted by the first six can hold a value a device, a
// credential store, a decrypted envelope, or an injector produced, and
// the seventh admits only what a built-in method's reviewed manifest names
// as an identifier: never a file's content, a mount's options, a user's
// GECOS text or a secret. So the guarantee is "no value the platform
// obtained, except identifiers a method declares recordable for its undo".
// This is still not a masking design and must never be described as one:
// there is nothing in an entry to mask, only identifiers to replay.
//
// # Kind 5 is the residual text channel
//
// DAGID, TaskName and Register are the only fields carrying bytes a
// runbook author typed, and that is recorded here as the design's one
// soft spot rather than argued away. DAGID is the mildest of the three:
// validRunbookID constrains it to ^[A-Za-z0-9_-]*$ (dag.go:26) and
// buildFromDef refuses a runbook whose id: fails that check (dag.go:504).
// A runbook's name: and register: are unconstrained free text, so an
// author who pastes a password into a task name puts it in the journal.
//
// Task.Params is excluded under the same rule, because the catalog
// documents param values as secret bearing: file.copy's content is a
// required param and is the file body. Kind 7 does not reach it: it reads
// only the undo a method emitted, and only the parameters that method's
// manifest declares.
//
// # Two archtests hold this up
//
// Neither is sufficient alone. TestJournalEntryHoldsNoValue rejects a
// field whose type could carry a value at all (map[string]interface{},
// any, error, []byte, an inventory type) anywhere in the transitive field
// graph, including as a slice element or a map value; InverseParam passes
// it because its value is a typed text, number or bool, never an any.
// TestEveryJournalStringFieldIsConstructed pins every string and []string
// field to the kind above that makes it safe, because reflection cannot
// tell a vector of key names from a vector of values, and the registry
// sweep (TestEveryProjectedKeyNameIsRegistryDeclared) lets a planted value
// survive only in InverseParams, only for a key its method declares.
//
// # Kind 7, taken 2026-09-28
//
// Section 12 of the Phase 40 design note proposed the seventh kind and
// asked that it be argued again before it was built. It was, by the
// user's rule of deciding on edge cases: a rollback from key names alone
// cannot undo a VM a run made without its author restating every name,
// and an authored undo list runs steps for tasks that never ran. The
// argument, and the parameters Section 12 undercounted (a user's comment,
// a mount's source and options, a symlink's old target), are recorded in
// the design note's Section 13.
//
// # The kind-2 fields are a contract projectLevel now keeps
//
// FQCN, InverseFQCN, StatKeys, ParamKeys and InverseParamKeys are
// documented below in the present tense (resolved through
// collection.Lookup, intersected against Doc.Returns) because that is the
// contract the projection satisfies. It is real code now, in
// journal_entry.go, and it is the only thing in this module that
// constructs a JournalEntry. Read those five comments as the
// specification it is held to, and resolveFQCN and admitKeys as what
// keeps it.
type JournalEntry struct {
	// JobID is the Walk-tier dispatch this execution belongs to (kind 1).
	// Empty on the Crawl tier, which has no dispatch at all. The Walk sink
	// stamps it at construction from the wire.DispatchPayload it was built
	// for, never from anything the run itself reports.
	JobID string `json:"job_id"`

	// Attempt is JetStream's own redelivery counter for that dispatch
	// (kind 1), so a second run against one device reads as a retry rather
	// than as two unrelated runs. Zero on the Crawl tier.
	Attempt int `json:"attempt"`

	// RunID identifies one Executor.Run call (kind 1). It is minted on
	// Run's own per-call state, not on Executor, which deliberately holds
	// no per-run state so a single Executor value stays safe to reuse or
	// to call Run on concurrently (see the run type, executor.go).
	RunID string `json:"run_id"`

	// Sequence orders the entries inside one run (kind 1). StartedAt
	// cannot do that job alone: a level fans out concurrently, so two
	// nodes can carry the same instant.
	Sequence int `json:"sequence"`

	// NodeID is the synthesized graph id this entry belongs to, for
	// example "tasks[0]" (kind 1), never the task's Register name. See
	// NodeResult.NodeID.
	NodeID string `json:"node_id"`

	// DAGID is the runbook's author-written id: field (kind 5), carried
	// through unchanged by buildFromDef. It is not a digest; only
	// DAGVersion is. See this type's own note on kind 5 for the one
	// constraint it does carry.
	DAGID string `json:"dag_id"`

	// DAGVersion is the compiled definition's content hash, formatted
	// "sha256:<hex>" (kind 4). It detects drift between the runbook that
	// ran and the runbook on disk now. It cannot recover that runbook's
	// content: nothing in this platform stores one.
	DAGVersion string `json:"dag_version"`

	// FQCN is the method this node ran, resolved through collection.Lookup
	// or the engine's own builtin table at write time (kind 2), never the
	// bytes an author wrote under fqcn:. Nothing checks fqcn: against the
	// registry at compile time (validateTask, tasktree.go, checks only
	// that exactly one of fqcn/block/parallel is set), and an unknown one
	// runs, fails, and reaches this journal. Copying it would put
	// arbitrary author text into the field the provenance rule calls a
	// catalog constant, on exactly the failure path the journal exists
	// for.
	FQCN string `json:"fqcn"`

	// FQCNUnresolved reports that resolution found nothing and FQCN holds
	// FQCNUnregistered (kind 3). The unresolved string is never stored,
	// and dropping it costs little: NodeID still names the graph position
	// and the operator still has the runbook.
	FQCNUnresolved bool `json:"fqcn_unresolved"`

	// ProviderProgram (kind 1) and ProviderDigest (kind 4) name the
	// external Collection program that provides FQCN's method, and the
	// digest it was loaded with, resolved at write time through the
	// registry exactly as FQCN is (collection.Descriptor.Provider, which
	// only the loader sets). Both are empty for a method compiled into this
	// binary. They say whose code did the work to a reader far from the
	// run; neither is text from the program, the runbook or a device.
	ProviderProgram string `json:"provider_program"`
	ProviderDigest  string `json:"provider_digest"`

	// TaskName is the task's author-written name: (kind 5), unconstrained
	// free text.
	TaskName string `json:"task_name"`

	// Register is the task's author-written register: name (kind 5),
	// unconstrained free text. It is the key a later task's when_cel reads
	// this node's result under, never the result itself.
	Register string `json:"register"`

	// DeviceID is the device this execution ran against (kind 1), the
	// inventory item's own stored id. Empty for a controller-side task,
	// which resolves no device at all.
	DeviceID string `json:"device_id"`

	// StartedAt and FinishedAt bound this execution (kind 1). They are
	// instants the executor stamps around its own call, never anything a
	// device reported. The event stream's own timestamp cannot serve here:
	// publish formats whole seconds (executor.go), too coarse to order a
	// concurrent fan-out.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`

	// Outcome is what this node did (kind 3).
	Outcome Outcome `json:"outcome"`

	// FailureStage names which stage failed (kind 3), read off the
	// executor's control flow rather than parsed back out of an error. It
	// is FailureStageNone unless Outcome is OutcomeFailed. The error's own
	// text is deliberately absent: NodeResult.Err embeds device output
	// verbatim, and the engine contractually never masks it
	// (executor_secrets_test.go asserts a raw secret survives in it).
	FailureStage FailureStage `json:"failure_stage"`

	// SkipKind names why this node was skipped (kind 3). It is
	// SkipKindNone unless Outcome is OutcomeSkipped. The skip reason
	// sentence is absent for the same reason the error text is: it quotes
	// the author's own expression back verbatim.
	SkipKind SkipKind `json:"skip_kind"`

	// SkipOrdinal and SkipTotal are which condition, of how many, decided
	// a condition skip (kind 6). SkipOrdinal is 1-based, and both are zero
	// when not applicable. evalAnd already computes both and formats them
	// into its reason sentence, and evalOr formats the total the same way
	// (conditional.go); the journal takes the numbers and leaves the
	// sentence, which quotes the author's own expression.
	//
	// The two do not always move together, and the case a reader hits
	// first is the one that breaks the pattern: a when_or skip carries a
	// SkipTotal with SkipOrdinal still zero. Under OR every item
	// evaluated false, so no single one is responsible and naming one
	// would be a fabricated attribution. That is the same reason evalOr's
	// own reason sentence names all of them. See SkipKindWhenOr.
	SkipOrdinal int `json:"skip_ordinal"`
	SkipTotal   int `json:"skip_total"`

	// StatKeys are the top-level keys of this node's stats that the
	// executing method's own Doc.Returns declares, plus sdk.StatInverse
	// and sdk.StatDiff (kind 2). Key names only: never a value, and never
	// a nested key, since a nested key can be a value one level up.
	StatKeys []string `json:"stat_keys"`

	// UndeclaredStatCount is how many top-level stat keys were not
	// admitted (kind 6). A rejected key is counted and never named,
	// because Doc is a documentation-generator struct that is partial by
	// construction (pkg/collection/doc.go), so an undocumented key is as
	// likely to be a documentation gap as a surprise. Counting keeps that
	// gap visible instead of dropping it silently.
	UndeclaredStatCount int `json:"undeclared_stat_count"`

	// ParamKeys are this task's param keys that the executing method's own
	// Doc.Params declares (kind 2). Param values are excluded outright and
	// no declaration can admit one: file.copy's content is a required
	// param and is the file body.
	ParamKeys []string `json:"param_keys"`

	// UndeclaredParamCount is how many param keys were not admitted
	// (kind 6), counted rather than named for the same reason as
	// UndeclaredStatCount.
	UndeclaredParamCount int `json:"undeclared_param_count"`

	// InverseFQCN is the method that undoes this one, resolved exactly as
	// FQCN is (kind 2). It gets that treatment for a stronger reason than
	// FQCN does: it is not even author text, but whatever a Collection
	// method put in a map[string]any at run time, and sdk.RecordInverse
	// validates only that it is non-empty (pkg/sdk/inverse.go:76).
	InverseFQCN string `json:"inverse_fqcn"`

	// InverseFQCNUnresolved reports that resolution found nothing and
	// InverseFQCN holds FQCNUnregistered (kind 3).
	InverseFQCNUnresolved bool `json:"inverse_fqcn_unresolved"`

	// InverseParamKeys are the resolved inverse target's param keys that
	// its own Doc.Params declares (kind 2): every key the undo carried,
	// whether or not its value is recorded in InverseParams.
	InverseParamKeys []string `json:"inverse_param_keys"`

	// UndeclaredInverseParamCount is how many inverse param keys were not
	// admitted (kind 6).
	UndeclaredInverseParamCount int `json:"undeclared_inverse_param_count"`

	// InverseParams are the undo's parameter values the emitting method's
	// manifest declares recordable (kind 7), in key order. A key appears
	// here only when the emitter is compiled into The Pleiades, declares
	// an undo through InverseFQCN (sdk.InverseSpec), lists the key in that
	// declaration's Record, and the target reads it; and only when the
	// value is a boolean, a number, or at most 256 bytes of valid,
	// terminal-safe text on one line. Anything else stays a key name in
	// InverseParamKeys, and InverseComplete says so.
	InverseParams []InverseParam `json:"inverse_params"`

	// InverseComplete reports that every parameter the undo carried is in
	// InverseParams and the undo is not partial (kind 3), so a rollback can
	// replay it exactly as the run recorded it. False for a task with no
	// undo.
	InverseComplete bool `json:"inverse_complete"`

	// InversePartial reports that the method marked its undo partial: it
	// would not put back everything the run overwrote (kind 3). A rollback
	// replays it only when its operator names it.
	InversePartial bool `json:"inverse_partial"`

	// DiffRecorded reports whether the method wrote a diff under
	// sdk.StatDiff (kind 3). Presence only, never the diff: a diff is a
	// before-and-after pair of exactly the device content this type
	// refuses to hold.
	DiffRecorded bool `json:"diff_recorded"`

	// ActionChanged reports that the task's action itself reported a
	// change (kind 3). It differs from Outcome changed exactly when a
	// stage after the action (register_mask, record) then failed: the
	// device was changed, the node failed, and a rollback must still undo
	// it.
	ActionChanged bool `json:"action_changed"`

	// AuthoredRollback reports that the task carries a rollback: list in
	// its runbook (kind 3), which a rollback runs instead of the recorded
	// undo. The steps themselves are the runbook's, never the journal's.
	AuthoredRollback bool `json:"authored_rollback"`

	// RollbackOf names the run this run undoes (kind 1): the run id on the
	// Crawl tier, the job id on the Walk tier. Empty for a run that is not
	// a rollback.
	RollbackOf string `json:"rollback_of"`

	// UndoesNode is the node, in RollbackOf, that this node undoes on the
	// same device (kind 1). Empty for a run that is not a rollback.
	UndoesNode string `json:"undoes_node"`

	// UndoesStep is which step of that node's undo this node is (kind 6):
	// 0 for a recorded undo, and the position in the task's rollback: list
	// for an authored one. A rollback resumed after a failure skips steps
	// already done.
	UndoesStep int `json:"undoes_step"`
}

// InverseParam is one recorded undo parameter (JournalEntry.InverseParams):
// its name and exactly one of Text, Number or Bool.
//
// The value is typed rather than an any, so nothing but a string, a
// number or a boolean can reach an entry, which is what
// TestJournalEntryHoldsNoValue checks for. A number is kept as its JSON
// text, so an integer as large as a uid or a memory size round-trips
// exactly.
type InverseParam struct {
	// Key is the parameter's name, from the emitting method's declaration
	// (kind 2).
	Key string `json:"key"`

	// Text is a string value (kind 7).
	Text *string `json:"text,omitempty"`

	// Number is a numeric value, as its JSON text (kind 7).
	Number *json.Number `json:"number,omitempty"`

	// Bool is a boolean value (kind 7).
	Bool *bool `json:"bool,omitempty"`
}

// Value returns the parameter's value as a runbook task's params carry it:
// a string, an int64 (or a float64 for a number with a fraction), or a
// bool; and false when it holds none, or a number that is not one.
func (p InverseParam) Value() (any, bool) {
	switch {
	case p.Text != nil:
		return *p.Text, true
	case p.Bool != nil:
		return *p.Bool, true
	case p.Number != nil:
		if n, err := p.Number.Int64(); err == nil {
			return n, true
		}
		if f, err := p.Number.Float64(); err == nil {
			return f, true
		}
	}
	return nil, false
}

// FQCNUnregistered is what JournalEntry.FQCN and JournalEntry.InverseFQCN
// hold when resolution found no registered method and no engine builtin.
// The caller's own unresolved bytes are never stored; the matching
// Unresolved flag is what says the name was dropped.
//
// It cannot collide with a real method name: collection.Register refuses
// any name that is not <namespace>.<method> with both halves non-empty
// (pkg/collection/collection.go:103-107), and this has no dot at all. So
// no registered descriptor can ever be confused with a dropped one.
const FQCNUnregistered = "<unregistered>"

// Outcome is what one node execution did. It is a closed set this package
// defines and the projection maps onto explicitly, never text copied off
// a result.
//
// The empty string is deliberately not a member. FailureStage and
// SkipKind each have a named zero value, because an entry legitimately
// carries neither of those most of the time; an entry with no Outcome is
// a projection bug, so there is no name for one to hide behind.
type Outcome string

// The outcomes one node execution can record.
const (
	// OutcomeRan is a node whose action ran and reported no change. It is
	// the "ok" status runOne already publishes (executor.go).
	OutcomeRan Outcome = "ran"

	// OutcomeChanged is a node whose action ran and reported altering real
	// state (ActionResult.Changed).
	OutcomeChanged Outcome = "changed"

	// OutcomeSkipped is a node that never ran at all: its
	// when/when_or/when_cel condition evaluated false, or its resolved
	// device was not admissible (LifecycleAdmits, admission.go). SkipKind
	// says which.
	OutcomeSkipped Outcome = "skipped"

	// OutcomeFailed is a node whose execution failed at one of the stages
	// FailureStage enumerates.
	OutcomeFailed Outcome = "failed"

	// OutcomeNotReached is the synthetic parallel fan-out/join marker
	// (TaskKindSynthetic), which short-circuits ahead of the whole
	// pipeline because it carries no runbook author's intent of its own.
	// It matches none of the four above, and gets its own value rather
	// than being folded into a default, so a reader never has to guess
	// whether an entry with no stats was a marker or a real task that did
	// nothing.
	//
	// It does not mean "a node a failed level stopped the run before".
	// Run breaks out of its level loop on a failure and never produces a
	// NodeResult for a later level, so there is nothing there to project
	// and no entry is written at all.
	OutcomeNotReached Outcome = "not_reached"
)

// FailureStage names which stage of a node execution failed. It is read
// off the executor's own control flow: every failure has its own call
// site with its own distinct wording, so the enum is structural rather
// than parsed.
//
// It is deliberately not read off the published event instead. Seven of
// the nine sites below publish a "failed" event and two do not (the
// workflow read at executor.go:583 and the condition evaluation at :603
// both return without publishing), so an event-derived stage would be
// silently absent for exactly those two. That gap is pre-existing and is
// not this type's to close.
//
// This answers "what failed" better than the error sentence does. It is
// queryable, it cannot drift when someone rewords an error string, and it
// is the only way to record a failure at all without storing
// NodeResult.Err, which embeds device output verbatim.
type FailureStage string

// The stages a node execution can fail at. There are nine, one per
// failure call site in executor.go, and the projection maps every one
// explicitly and fails closed on any branch it does not recognize
// (validFailureStage, journal_entry.go).
//
// Each constant below cites the executor.go line that tags a result with
// it. Those line numbers rot whenever executor.go moves, and nothing in
// this repository checks them, so treat the constant's own name as the
// durable anchor: each one appears at exactly one call site, so a search
// for it finds that site whatever line it has moved to.
const (
	// FailureStageNone is the zero value: this entry is not a failure.
	FailureStageNone FailureStage = ""

	// FailureStageWorkflowRead is "failed to read workflow context"
	// (executor.go:583): the condition needed a WorkflowContext snapshot
	// and reading it failed.
	FailureStageWorkflowRead FailureStage = "workflow_read"

	// FailureStageConditionEval is "failed to evaluate condition"
	// (executor.go:603): the compiled when/when_or/when_cel program
	// errored, which is distinct from evaluating cleanly to false.
	FailureStageConditionEval FailureStage = "condition_eval"

	// FailureStageSecretMask is "failed to apply secret_mask"
	// (executor.go:629), which runs once per node before device
	// resolution (executor_secrets.go).
	FailureStageSecretMask FailureStage = "secret_mask"

	// FailureStageRender is "failed to render the parameters of"
	// (executor.go, runNode): a parameter holding a template read an
	// undefined name, failed a filter, or had no renderer to render it. It
	// runs before any device is touched.
	FailureStageRender FailureStage = "render"

	// FailureStageResolveTarget is "failed to resolve target"
	// (executor.go:636), including the case of a named target that
	// matches no inventory host or tag.
	FailureStageResolveTarget FailureStage = "resolve_target"

	// FailureStageLockAll is "failed to acquire all locks up front"
	// (executor.go:692): the AcquisitionAllAtPlanTime strategy's
	// all-or-nothing acquisition, which fails the whole node.
	FailureStageLockAll FailureStage = "lock_all"

	// FailureStageLockDevice is "failed to acquire lock on device"
	// (executor.go:778): the AcquisitionPerDeviceAsReached default's own
	// per-device acquisition, which fails one device.
	FailureStageLockDevice FailureStage = "lock_device"

	// FailureStageAction is the ActionExecutor.Execute call itself
	// returning an error (executor.go:806). This is the common case and
	// the only one that reaches a device.
	FailureStageAction FailureStage = "action"

	// FailureStageRegisterMask is register_mask failing over this task's
	// own just-computed result (executor.go:816), before anything records
	// it.
	FailureStageRegisterMask FailureStage = "register_mask"

	// FailureStageRecord is "failed to record result of task"
	// (executor.go:823): merging a Register'd result into
	// WorkflowContext failed.
	FailureStageRecord FailureStage = "record"
)

// SkipKind names why a node was skipped. The three condition values match
// ConditionProgram's own keyword exactly ("when", "when_or", "when_cel",
// set at compileItems' only three call sites, conditional.go), so the
// projection maps a keyword straight across rather than inventing a
// second vocabulary that could drift from it.
type SkipKind string

// The reasons a node execution can be skipped.
const (
	// SkipKindNone is the zero value: this entry is not a skip.
	SkipKindNone SkipKind = ""

	// SkipKindWhen is a when: list that evaluated false. Every item must
	// hold, so SkipOrdinal names the first item that did not.
	SkipKindWhen SkipKind = "when"

	// SkipKindWhenOr is a when_or: list where every item evaluated false.
	// Each one contributed, so SkipOrdinal is not meaningful on its own
	// here and SkipTotal is what matters.
	SkipKindWhenOr SkipKind = "when_or"

	// SkipKindWhenCEL is a when_cel: expression that evaluated false.
	// There is only ever one, so the ordinal and total are both 1.
	SkipKindWhenCEL SkipKind = "when_cel"

	// SkipKindLifecycle is a resolved device whose lifecycle state does
	// not admit real work (LifecycleAdmits, admission.go). Unlike a
	// condition skip, this entry keeps DeviceID populated: naming which
	// device caused it is the whole point of the skip.
	SkipKindLifecycle SkipKind = "lifecycle"
)

// Journal is the durable record of what a run did. Executor.Run is its
// only writer: one Record call per topological level, carrying every
// entry that level produced.
//
// Run calls it from its level loop, immediately after the level's results
// have joined and before the scan that may end the walk, so the level a
// failure ended on is recorded rather than lost (recordLevel,
// journal_entry.go).
//
// It is a port rather than a direct import for the same reason
// TargetResolver is one (action.go): a concrete sink lives outside this
// package, and the Walk-tier sink reaches storage, so importing one here
// would put a storage dependency inside the executor. The composition
// roots wire the sink (cmd/pleiades/run.go for the Crawl tier,
// internal/adapters/native for the Walk tier), which is their job.
// NewExecutor defaults to a no-op sink, so a caller wiring none keeps its
// exact prior behavior.
type Journal interface {
	// Record writes entries durably.
	//
	// The context is detached from the run's own. A caller passes
	// context.WithoutCancel(runCtx) plus the sink's own timeout, never
	// runCtx itself. On the Runner the context that reaches Executor.Run
	// descends from the process context (cmd/runner/main.go:150, canceled
	// on SIGINT or SIGTERM at :350-355) by way of executeWithLease's
	// execCtx, which propagates that cancellation for an interruptible
	// payload and is canceled unconditionally by its own deferred
	// cancelExec once the dispatch ends (internal/runner/agent_exec.go:
	// 116-146). A Record that inherited it would therefore fail exactly
	// when the record matters most: a graceful shutdown would discard the
	// journal of every level that had already completed. WithoutCancel
	// keeps the context's values, so an active OpenTelemetry span still
	// crosses the boundary and only the cancellation is dropped;
	// internal/api/readiness.go:89-99 draws the same seam for the same
	// reason.
	//
	// A Record error must never fail the run. Run logs it at error with
	// the run, the DAG, the level and the devices, counts it, and returns
	// the run's own outcome unchanged. Deliberately NOT the job: JobID is
	// the Walk sink's to stamp from the dispatch it was built for, and
	// this package never sees one (see recordLevel, journal_entry.go). This is not politeness about an observability
	// side effect: on the Walk tier an execution error is not merely
	// reported, it routes into event.HandleDeliveryFailure, which Naks
	// for redelivery below maxDeliver (internal/runner/agent_handle.go:
	// 184-200), so the runbook runs again against the same device. A
	// failed audit write turning into a repeated configuration change is
	// far worse than a missing audit row. It is not swallowed silently
	// either: the log line and the counter are what keep a sink that is
	// failing every write from looking like a run that journaled nothing.
	// An implementation must be safe for concurrent use. A single
	// Executor value is documented as safe to reuse and even to call Run
	// on concurrently (see the run type, executor.go), the sink is held
	// on the Executor rather than per run, and each such Run writes
	// through this same value at its own level barriers. Two Records can
	// therefore be in flight at once, carrying different RunIDs. That is
	// the property the per-call RunID exists to keep readable, and it is
	// stated here because a sink author reading only the sentence above
	// about one call per level would reasonably conclude the opposite.
	Record(ctx context.Context, entries []JournalEntry) error
}

// noopJournal is the default Journal: it accepts every entry and keeps
// none. It exists so Executor's journal field is never nil, which means
// Run needs no nil check at a level boundary, and so every NewExecutor
// call site that predates the journal keeps its exact prior behavior
// instead of having to opt out of something new.
type noopJournal struct{}

// Record implements Journal by discarding entries and reporting success.
// Discarding is the contract here rather than a failure: a caller that
// wired no sink asked for no journal.
func (noopJournal) Record(context.Context, []JournalEntry) error { return nil }

// WithJournal writes this Executor's run journal to j, one Record call
// per topological level. It matches WithVariables and WithTaskTimeout:
// every existing NewExecutor call site that sets none of them keeps
// compiling and behaving exactly as before, since the default sink keeps
// nothing.
//
// A nil j leaves the no-op default in place rather than installing a nil
// interface value, so a composition root that builds its sink
// conditionally (a Crawl-tier run with no writable directory, say) cannot
// turn "no journal configured" into a panic at the first level boundary.
// This is a deliberate difference from WithVariables, which accepts a nil
// map because runNode already substitutes an empty one on every call; a
// nil Journal has no such per-call fallback, so refusing it once here is
// cheaper than checking for it at every Record site forever.
//
// The guard covers a nil INTERFACE VALUE and not a typed nil, and the
// difference is worth stating because it falls exactly on the
// conditional-construction case above. This is caught:
//
//	var j engine.Journal
//	if dir != "" { j = journal.NewFileStore(dir) }
//	engine.WithJournal(j)
//
// This is not, because a non-nil interface holding a nil *FileStore is
// not itself nil:
//
//	var s *journal.FileStore
//	if dir != "" { s = journal.NewFileStore(dir) }
//	engine.WithJournal(s)
//
// So a composition root declares the variable as engine.Journal, not as
// the concrete sink. The guard is not widened with reflection to cover
// the second form: a typed nil reaching here is a bug in the wiring, and
// a rule that silently absorbs it trades a panic naming the exact
// composition root for a run that journals nothing and says nothing
// about why, which is the worse of the two failures for an audit
// feature. Section 25 puts wiring correctness in cmd/, and this is a
// wiring error.
func WithJournal(j Journal) ExecutorOption {
	return func(x *Executor) {
		if j == nil {
			return
		}
		x.journal = j
	}
}
