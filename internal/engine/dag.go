package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
)

// validRunbookID matches an empty string or one made up only of letters,
// digits, hyphens, and underscores. WorkflowDef.ID is embedded directly
// into a NATS subject string (Executor.publish, executor.go:
// "pleiades.events.workflow.<id>.node.<node-id>"), so an unconstrained ID
// containing "." (the subject-token delimiter), "*" (a single-token
// wildcard), or a trailing ">" (a multi-token wildcard) could widen or
// misroute a subject beyond what the publisher intended: an id of
// "billing.exfil" would publish under the same subject prefix a scoped
// subscriber watching "pleiades.events.workflow.billing.>" also matches,
// even though the two are unrelated runbooks. This was found and proven
// empirically (Phase 39, Schema & Injection Hardening; FAILURE_PATTERNS.md
// #18), not by inspection alone. Rejected at buildFromDef, the single
// domain-level compilation path every surface format shares, rather than
// only at the point of use, so no future caller of dag.ID can reintroduce
// the same gap by skipping a check.
var validRunbookID = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)

// Metadata is the polymorphic, additive, native-Pleiades-only section of a
// WorkflowDef. Ansible playbooks have no equivalent concept: this is where
// workflow-level, Pleiades-specific concerns that are not part of the core
// task shape land, so new fields can be added here over time without ever
// touching Task or WorkflowDef's core shape.
//
// BlastRadius is deliberately NOT a field here. Blast radius is always
// computed by the engine from resolved targets and inventory tier data, it
// is never authored by hand, so it has no YAML or JSON representation on
// Metadata itself.
type Metadata struct {
	// ServiceEffecting marks a runbook as one whose execution can affect
	// live service, as opposed to a purely read-only or diagnostic run.
	ServiceEffecting bool `json:"service_effecting,omitempty" yaml:"service_effecting,omitempty"`

	// Interruptible marks whether execution of this runbook may be safely
	// self-aborted by a Runner that has lost its heartbeat with the
	// Controller before the Controller's own lock TTL expires (PLAN.md
	// Section 16's Network Partitions mitigation: "If a Runner loses
	// heartbeat with the Controller, it self-aborts execution before the
	// Controller TTL expires. Exception: Un-abortable tasks
	// (interruptible: false) finish execution, and the Controller
	// quarantines the device instead of re-issuing the lock.").
	//
	// nil (the field omitted entirely) means interruptible, the safe
	// default: PLAN.md Section 16 frames interruptible: false as the
	// named exception, which only makes sense if the unmarked case is the
	// common, abortable one. A plain bool's zero value (false) would
	// silently invert that default for every runbook that never sets this
	// field, which is why this is a pointer rather than a bool, unlike
	// ServiceEffecting above (whose safe default *is* false, so a plain
	// bool costs it nothing). Use IsInterruptible, not this field
	// directly, so no caller has to re-derive the nil-means-true rule
	// itself.
	Interruptible *bool `json:"interruptible,omitempty" yaml:"interruptible,omitempty"`

	// Description is a sentence or two about what this runbook does, for a
	// reader browsing the catalog who has not opened the file. There is no
	// Ansible play-level equivalent, which is why it lives here rather
	// than at the top level: Metadata is where Pleiades-specific
	// workflow-level concerns land.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Category is the single bucket this runbook is filed under, for
	// grouping a catalog that has grown past the point of being one flat
	// list ("patching", "compliance", "network"). Single rather than a
	// list, because a thing filed in three places is a thing nobody can
	// find twice in a row; use Labels for the many-to-many axis.
	Category string `json:"category,omitempty" yaml:"category,omitempty"`

	// Labels are free-form markers for filtering the catalog.
	//
	// Called labels rather than tags, deliberately, and this is the one
	// naming decision here worth defending. Ansible already has tags:, and
	// it means something specific and different -- which tasks --tags and
	// --skip-tags select at run time. The Pleiades aims to be a strict
	// superset of Ansible playbooks, so that meaning is reserved, and
	// spending the word on catalog filtering would make a future
	// implementation of real Ansible tags either impossible or
	// gratuitously incompatible.
	//
	// AWX made exactly this distinction already and it is worth copying
	// rather than re-deriving: an AWX Job Template has Labels for
	// organizing and filtering, while tags remain Ansible's task selector.
	// Same two concepts, same two words, no collision.
	Labels []string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// IsInterruptible reports whether m's runbook is safe to self-abort,
// applying Interruptible's own documented "nil means true" default in
// exactly one place.
func (m Metadata) IsInterruptible() bool {
	return m.Interruptible == nil || *m.Interruptible
}

// WorkflowDef represents a user runbook. The yaml and json tags agree
// field for field, so a hand-written YAML runbook and a JSON-built one
// decode into the identical Go value and therefore compile to the
// identical *DAG (Part 0 Phase W2's bidirectional-compilation requirement).
//
// The Pleiades aims to be a strict superset of Ansible playbooks with a minimum
// barrier to entry, so a runbook is authored the way an Ansible playbook
// is: an ordered pretasks/tasks/posttasks list, not a hand-wired graph of
// nodes and edges. PreTasks and PostTasks give authors the same "setup,
// body, teardown" shape Ansible plays already use; Tasks is the only
// required list, matching Ansible where a play with no tasks: at all is
// unusual but pretasks/posttasks-only plays exist.
type WorkflowDef struct {
	ID string `json:"id" yaml:"id"`

	// Name is this runbook's human title, exactly like an Ansible play's
	// own name:. It sits at the top level rather than under Metadata
	// precisely because Ansible puts it there: a play lifted unchanged
	// into this platform keeps its name, which is the whole migration
	// on-ramp this format exists to offer.
	//
	// Empty means the catalog falls back to ID, so a runbook that never
	// sets one is displayed by the identifier it is dispatched by rather
	// than by a blank row.
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Hosts names this runbook's default target, exactly like an Ansible
	// play's own hosts:: a device name or an inventory tag string, resolved
	// the same way a task's Params["target"] already is (TargetResolver,
	// action.go). It is a default, not an override: a task that sets its
	// own non-empty Params["target"] still wins, the same "most specific
	// level wins" hierarchical policy AGENTS.md's Architecture Principles
	// already establish for every other multi-level setting in this
	// codebase. This keeps the one capability per-task target has and
	// Ansible's single-hosts-per-play model does not: a task with no
	// target at all (a controller-side action) still runs controller-side
	// even when Hosts is set, and a task can still name a different device
	// than the rest of the runbook, matching PLAN.md Section 14's mixed
	// target-side/controller-side/hybrid execution contexts in one play.
	// See TaskTarget (action.go), the single place this default/override
	// resolution happens, reused by both the executor and every
	// validate.Rule that resolves a target. Empty means every task must
	// name its own target explicitly, exactly today's behavior.
	Hosts string `json:"hosts,omitempty" yaml:"hosts,omitempty"`

	// Type is the runbook-type discriminator. It reuses the field name
	// PLAN.md Section 23 already specifies for distinguishing native
	// versus Ansible content in a shared GitOps repository, and both this
	// implementation and Section 23 agree on the same value, "native",
	// for the same concept: the value names what the format is, not what
	// the product is called, so it does not need to change again if the
	// product is renamed a second time. `type: ansible` is reserved for
	// the future Ansible interop path (PLAN.md Section 23) but is not
	// actionable yet: no native Ansible execution path exists in this
	// repository. An absent or empty Type is treated as "native", so
	// every existing runbook, including the CLI's scaffolded sample,
	// keeps working unchanged.
	Type string `json:"type,omitempty" yaml:"type,omitempty"`

	// Metadata carries the additive, native-Pleiades-only, workflow-level
	// section described on the Metadata type. It has no Ansible
	// equivalent and is entirely optional.
	Metadata Metadata `json:"metadata,omitempty" yaml:"metadata,omitempty"`

	// CheckMode, Ansible's check_mode, makes the whole run a check,
	// whatever mode it was started in. It can only narrow: see
	// CheckModeFlag for what is accepted and why false is refused.
	CheckMode CheckModeFlag `json:"check_mode,omitempty" yaml:"check_mode,omitempty"`

	// Tags applies to every task in the runbook, as a play's tags do in
	// Ansible: each task inherits them (propagateTags, tags.go).
	Tags TagList `json:"tags,omitempty" yaml:"tags,omitempty"`

	// Reversible asks that every task that can change a device be one a
	// rollback can undo: a method whose recorded undo replays whole, a
	// read-only method, or a task with an authored rollback: list.
	// Validation refuses the runbook otherwise (ReversibleRule), before
	// anything runs. False, the default, asks nothing.
	Reversible bool `json:"reversible,omitempty" yaml:"reversible,omitempty"`

	// PreTasks runs before Tasks, in order. It is the runbook's setup
	// phase, mirroring an Ansible play's pre_tasks:.
	PreTasks []Task `json:"pretasks,omitempty" yaml:"pretasks,omitempty"`

	// Tasks is the runbook's main ordered task list, mirroring an
	// Ansible play's tasks:.
	Tasks []Task `json:"tasks" yaml:"tasks"`

	// PostTasks runs after Tasks, in order. It is the runbook's teardown
	// phase, mirroring an Ansible play's post_tasks:.
	PostTasks []Task `json:"posttasks,omitempty" yaml:"posttasks,omitempty"`
}

// Task is a single step in a runbook, or a grouping of steps. Mirroring
// Ansible, a Task is either a leaf task, a module call identified by FQCN
// and optionally carrying Params and Register, or a block task, a Block of
// child tasks optionally paired with Rescue and Always handlers, never
// both at once. See validateTask for the rules this shape enforces.
type Task struct {
	// Name is a free-form human label, exactly like Ansible's task name:.
	// It has no uniqueness requirement: it exists for a human reading the
	// runbook or an error message, never for machine identity.
	Name string `json:"name" yaml:"name"`

	// Conditional holds this task's optional skip condition (when,
	// when_or, or when_cel). The yaml inline tag flattens it into the
	// surrounding YAML map; encoding/json promotes an untagged anonymous
	// struct field's exported fields automatically, which achieves the
	// same flattening for JSON with no extra code. This type is reused
	// unchanged from the prior session's per-node/per-edge conditional
	// work; see conditional.go.
	Conditional `yaml:",inline"`

	// FQCN names the action this leaf task performs, e.g. "ssh_exec",
	// "ios_backup", or "noop". The name mirrors Ansible's fully-qualified
	// collection name terminology, but no collection/namespace resolution
	// is implemented anywhere in this codebase: FQCN accepts exactly the
	// same bare-string action vocabulary the old Action field did,
	// nothing more. Empty on a block task.
	FQCN string `json:"fqcn,omitempty" yaml:"fqcn,omitempty"`

	// Params holds this task's arguments. By convention, an optional
	// "target" key names a device or an inventory tag string used to
	// resolve which inventory devices this task applies to.
	Params map[string]interface{} `json:"params,omitempty" yaml:"params,omitempty"`

	// Register names a variable this task's result is stored under, for
	// later tasks to reference, mirroring Ansible's register:.
	Register string `json:"register,omitempty" yaml:"register,omitempty"`

	// Within bounds a target chosen from data (Phase 117a): a device name or
	// inventory tag, written literally, naming the only devices a rendered
	// params.target may resolve to. The builder requires it beside a target
	// holding a template and refuses it anywhere else; plan-time checks run
	// against every device it names (TaskTarget), and at dispatch the
	// rendered target must name devices inside it, by name, never by tag
	// (resolveBounded).
	Within string `json:"within,omitempty" yaml:"within,omitempty"`

	// CheckMode, Ansible's check_mode, runs this task (and, on a block,
	// its block, rescue and always tasks) in check mode even in a real
	// run: its method's Check runs instead of its Invoke, and nothing it
	// covers is journaled. The builder copies a runbook's or a block's
	// key down onto every task it covers (propagateCheckMode), so the
	// executor reads only this field. See CheckModeFlag.
	CheckMode CheckModeFlag `json:"check_mode,omitempty" yaml:"check_mode,omitempty"`

	// Tags are the names --tags and --skip-tags select this task by. A
	// block's or parallel group's tags pass down to its children, and a
	// task tagged never runs only when a run names one of its tags
	// (tags.go).
	Tags TagList `json:"tags,omitempty" yaml:"tags,omitempty"`

	// RegisterMask names fields of this task's own ActionResult.Stats (once
	// computed), dotted paths into nested values allowed, whose values must
	// be treated as secret from this point on: masked out of every later
	// published event and out of a caller's own printed output, wherever
	// that value reappears for the rest of the run. Named for what it does,
	// register_mask: applied at the moment this task's own result is
	// registered, before Merge or publish ever see it (executor.go's
	// markRegisterMask call site), the same "secret from the instant it is
	// produced" guarantee a password field gets. A path may optionally be
	// prefixed with this task's own Register name (e.g. Register
	// "running_config", path "running_config.stdout"), mirroring when_cel's
	// stat.<register> addressing; markRegisterMask strips that exact prefix
	// before resolving, so both the prefixed and bare ("stdout") spelling
	// reach the identical field, and neither is silently a no-op. StringList
	// (not a plain []string) so a single path can be written as a bare
	// scalar, matching When/WhenOr's own scalar-or-list convenience, since a
	// hand-typed one-mask task is the common case. This is Ansible parity
	// Ansible itself does not have (there is no per-value secrecy in a
	// registered result, only a whole-task no_log), for the case where a
	// value's secrecy is only known at runtime (a generated password, a
	// dynamically issued token): see internal/redact, whose substring
	// masking this reuses, for the "known, pre-registered secret" case
	// this is deliberately not. (It cited internal/credential.Mask until
	// this phase corrected it; that function was deleted in Phase 22.) See executor_secrets.go for how these are collected
	// and applied, and secret_mask.go's SecretMaskSpec for the different,
	// deliberately separate retroactive case (marking an earlier task's
	// already-registered result secret, flat top-level fields only, no
	// nested-path support): the two do not share an implementation, and
	// RegisterMask's nested-path support does not extend to SecretMaskSpec.
	RegisterMask StringList `json:"register_mask,omitempty" yaml:"register_mask,omitempty"`

	// SecretMask retroactively marks fields of an earlier task's already
	// registered result as secret, evaluated once for this task (not once
	// per resolved device) before this task's own action runs. See
	// SecretMaskSpec (secret_mask.go) and executor_secrets.go.
	SecretMask *SecretMaskSpec `json:"secret_mask,omitempty" yaml:"secret_mask,omitempty"`

	// LockAcquisition selects when this task's device locks are acquired
	// (PLAN.md Section 13's two acquisition strategies). The zero value,
	// AcquisitionPerDeviceAsReached, is today's only behavior and needs
	// no runbook change to keep. See AcquisitionStrategy's own doc
	// comment (lock_acquisition.go) for why this is a literal per-task
	// opt-in, not the hierarchical policy resolver.
	LockAcquisition AcquisitionStrategy `json:"lock_acquisition,omitempty" yaml:"lock_acquisition,omitempty"`

	// Block holds this task's child tasks, if it is a block task rather
	// than a leaf task. Its children run in order, forming the happy
	// path that splices into the position this block task occupies; see
	// DAG.Adjacency.
	Block []Task `json:"block,omitempty" yaml:"block,omitempty"`

	// Rescue holds tasks to run if a task in Block fails, mirroring
	// Ansible's rescue:. Only meaningful alongside a non-empty Block.
	Rescue []Task `json:"rescue,omitempty" yaml:"rescue,omitempty"`

	// Always holds tasks to run unconditionally after Block (and Rescue,
	// if it ran), mirroring Ansible's always:. Only meaningful alongside
	// a non-empty Block.
	Always []Task `json:"always,omitempty" yaml:"always,omitempty"`

	// Parallel holds this task's children, if it is a parallel task
	// rather than a leaf or block task (mutually exclusive with FQCN and
	// Block; see validateTask). Its children all run concurrently rather
	// than in sequence, mirroring PLAN.md Section 14's parallel:
	// construct; synthesizeParallel (tasktree.go) splices each child's
	// own chain between a synthetic fan-out and join node rather than
	// stitching them into one another the way Block's children are.
	// Rescue and Always stay Block-only: Ansible has no established
	// parallel-failure-handling vocabulary to mirror, so a Parallel task
	// carrying either is rejected rather than given invented semantics.
	Parallel []Task `json:"parallel,omitempty" yaml:"parallel,omitempty"`

	// Rollback is this task's authored undo: plain method tasks that run,
	// on each device this task changed, when a rollback of the run undoes
	// it, in place of the undo the method recorded (Phase 40). They never
	// run in the forward run and are not nodes of its DAG; validateTask
	// holds them to a plain method call each (validateRollback).
	Rollback []Task `json:"rollback,omitempty" yaml:"rollback,omitempty"`

	// synthetic marks a structural fan-out/join marker node the Builder
	// constructs itself (synthesizeParallel), never one a runbook author
	// writes. It is unexported so it is unreachable from encoding/json or
	// yaml.v3 decode: only this package's own internal construction
	// (registerSyntheticNode) ever sets it. See TaskKindSynthetic.
	synthetic bool
}

// DAG is the executable in-memory graph compiled from a WorkflowDef.
type DAG struct {
	ID       string
	Metadata Metadata

	// CheckMode is the runbook's own check_mode key: the whole run is a
	// check, whatever mode the Executor was given (WithMode). The builder
	// has also copied it onto every task.
	CheckMode bool

	// Reversible is the runbook's own reversible: key (WorkflowDef),
	// which validation's ReversibleRule holds every task to.
	Reversible bool

	// Name is def.Name, carried through unchanged so a compiled runbook
	// keeps the title its file gave it. The catalog reads it from here
	// rather than re-parsing the source, which is what stops a listing and
	// a run disagreeing about which runbook this is.
	Name string

	// Hosts is def.Hosts, carried through unchanged from the WorkflowDef
	// this DAG was compiled from. See WorkflowDef.Hosts for the
	// default/override semantics and TaskTarget (action.go) for where it
	// is applied.
	Hosts string

	// Version is a content-hash digest of the fully-resolved WorkflowDef
	// this DAG was compiled from (computed in buildFromDef, after
	// resolveImportTasks has expanded every import_tasks task in place),
	// formatted "sha256:<hex>" to match this repo's own existing
	// content-hash conventions (internal/topology.DurableName) and the
	// OCI digest format Phase 43 (OCI Distribution) already commits to
	// elsewhere. Hashing the resolved definition, not the raw top-level
	// file's own bytes, is deliberate: two runbooks that reach the
	// identical effective task tree via different import_tasks paths hash
	// identically, and editing an imported sub-file changes the
	// importing runbook's own Version too - this is what makes Version a
	// real "pin the compiled definition" identity (PLAN.md Section 25's
	// Workflow Definition Versioning), not just a hash of one file's
	// bytes. It is computed, never authored: WorkflowDef has no version:
	// field, matching Task.Kind's own "derived, not a second source of
	// truth" reasoning.
	Version string

	// PreTasks, Tasks, and PostTasks are the original, unflattened task
	// lists this DAG was compiled from, preserved for callers that want
	// the authored tree shape rather than the flattened Nodes view.
	PreTasks  []Task
	Tasks     []Task
	PostTasks []Task

	// Nodes is the flattened view of every task in the runbook, including
	// every Block, Rescue, Always, and Parallel descendant at any nesting
	// depth, plus each Parallel task's own synthetic fan-out/join marker
	// nodes (TaskKindSynthetic), keyed by its synthesized ID (see
	// synthesizeChain, synthesizeParallel, and collectSubtree). This is
	// what validation and capability-checking walk, since it covers
	// every task, not just the ones on the happy path.
	Nodes map[string]*Task

	// Adjacency holds the "happy path" chain only: PreTasks in order,
	// then Tasks in order, then PostTasks in order. Within a block task,
	// its Block children chain in order and splice into the position
	// their parent block task occupies, so the block task's own ID never
	// appears as a source or target in Adjacency; a parallel task's
	// children splice in the same way, between its own synthetic
	// fan-out/join pair (synthesizeParallel), so the parallel task's own
	// ID never appears here either. A consequence validateTask now
	// enforces at write time rather than leaving implicit here: a
	// when/when_or/when_cel or secret_mask set directly on a block or
	// parallel task would compile into Conditions/Nodes but never be
	// consulted, since nothing in the executor's walk ever visits that
	// ID; validateTask rejects it instead, naming the child tasks inside
	// as where it belongs. Rescue and Always children are
	// deliberately NOT part of this chain: EdgeType (this file) now gives
	// "run this on failure" a real vocabulary to be expressed as an edge,
	// but Executor does not yet interpret it (see EdgeType's own doc
	// comment for why that is a separate, named follow-up rather than
	// silently folded in here). Rescue/Always still get entries in Nodes
	// and Conditions, so validation and capability-checking still cover
	// them, just not here.
	Adjacency map[string][]EdgeConfig

	// Conditions holds every task's compiled when/when_or/when_cel
	// condition, keyed by the same synthesized ID used in Nodes and
	// Adjacency. An absent or nil entry means unconditional. Unlike a bare
	// Program, evaluating a ConditionProgram that comes out false also
	// returns a human-readable reason naming the responsible expression
	// (see ConditionProgram, conditional.go).
	Conditions map[string]*ConditionProgram

	// EntryPoint is the synthesized ID of the first node in the overall
	// happy-path chain: the entry of whichever of PreTasks, Tasks, and
	// PostTasks comes first and is non-empty. It is the empty string only
	// when all three are empty, meaning this DAG contributes no chain at
	// all. TopologicalOrder walks Adjacency starting here so that
	// Rescue, Always, and a block task's own superseded ID (which never
	// appear as a source or target in Adjacency, see above) are excluded
	// from the returned order even though they still appear in Nodes and
	// Conditions.
	EntryPoint string

	// Selection is the tag filter this DAG's Nodes, Adjacency and
	// Conditions were projected with (Select, select.go). The builder
	// applies the zero value, Ansible's default: every task except one
	// tagged never.
	Selection TagFilter

	// full is every task and compiled condition the builder produced,
	// before any tag selection, so Select can project the same runbook
	// again with another filter without rebuilding it. Nil for a DAG
	// assembled by hand, which Select then treats as its own full set.
	full *fullGraph
}

// EdgeType classifies an Adjacency edge by which of its source node's
// outcomes it is traversed for, giving PLAN.md Section 22.1's status
// routing ("Node A dispatches, then traverses to Node B on success or Node
// C on failure") a real vocabulary to be expressed in.
//
// This phase (10) adds the vocabulary only, not execution-time branching:
// every edge synthesizeChain/synthesizeParallel produce today is
// EdgeTypeOnSuccess (the zero value), so no existing runbook's behavior
// changes. Executor does not yet interpret EdgeTypeOnFailure or
// EdgeTypeAlways, and Task.Rescue/Task.Always deliberately still do not
// appear in Adjacency at all (see DAG.Adjacency's own doc comment) -
// wiring either in is a named, separate follow-up: LevelIterator computes
// static, outcome-independent reachability once up front
// (reachableWithInDegree, topology.go), and Executor.Run aborts its whole
// walk on any node failure rather than routing around it (executor.go),
// so making a failure actually route to a different next node changes
// both, not just adds a field here.
type EdgeType int

const (
	// EdgeTypeOnSuccess traverses only if the source node succeeded. It
	// is the zero value and today's only behavior: every synthesized
	// happy-path edge is implicitly this.
	EdgeTypeOnSuccess EdgeType = iota

	// EdgeTypeOnFailure traverses only if the source node failed. Not yet
	// interpreted by Executor; see EdgeType's own doc comment.
	EdgeTypeOnFailure

	// EdgeTypeAlways traverses unconditionally, regardless of outcome.
	// Not yet interpreted by Executor; see EdgeType's own doc comment.
	EdgeTypeAlways
)

// String returns EdgeType's name, used in error messages and logs.
func (e EdgeType) String() string {
	switch e {
	case EdgeTypeOnSuccess:
		return "on_success"
	case EdgeTypeOnFailure:
		return "on_failure"
	case EdgeTypeAlways:
		return "always"
	default:
		return "unknown"
	}
}

// EdgeConfig is a single synthesized structural connector in DAG.Adjacency.
// Synthesized edges carry no CEL condition of their own (every conditional
// lives on Task via the embedded Conditional, compiled into DAG.Conditions
// keyed by node ID, never on an edge), only a Type classifying which
// outcome it is meant to be traversed for. Every edge this package
// synthesizes today is EdgeTypeOnSuccess, its zero value; EdgeConfig lives
// on the compiled DAG output, never on the authored WorkflowDef/Task
// input, so it needs no YAML/JSON (un)marshal support - nothing decodes
// one from user input.
type EdgeConfig struct {
	To   string
	Type EdgeType
}

// Builder compiles JSON into an executable memory DAG.
type Builder struct {
	cel Evaluator
}

// NewBuilder initializes a new Workflow DAG builder.
func NewBuilder(celEvaluator Evaluator) *Builder {
	return &Builder{
		cel: celEvaluator,
	}
}

// Build parses a raw JSON payload, validates references, compiles CEL expressions,
// and ensures the graph is acyclic. It has no file context, so an
// import_tasks task in payload fails with a clear error; use
// BuildFromYAMLFile for a runbook that uses import_tasks.
func (b *Builder) Build(payload []byte) (*DAG, error) {
	// Before the sugar rewrite, which decodes into a map and so would
	// quietly keep only the last of two repeated keys.
	if _, err := parseJSONKeyTree(payload); err != nil && !errors.Is(err, errNotJSON) {
		return nil, err
	}
	normalized, err := normalizeWorkflowJSON(payload)
	if err != nil {
		return nil, err
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &top); err == nil {
		keys := make([]string, 0, len(top))
		for k := range top {
			keys = append(keys, k)
		}
		if err := checkRunbookKeys(keys); err != nil {
			return nil, err
		}
	}
	// Exact-case keys at every level (encoding/json would match "FQCN" to
	// fqcn). A payload the tree reader cannot parse is left to the decoder
	// below, which reports it in its own words.
	if tree, err := parseJSONKeyTree(normalized); err == nil {
		if err := checkKnownKeys(tree, reflect.TypeFor[WorkflowDef](), "json", ""); err != nil {
			return nil, err
		}
	}

	var def WorkflowDef
	dec := json.NewDecoder(bytes.NewReader(normalized))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to unmarshal JSON: trailing data after the runbook")
	}
	return b.buildFromDef(def, "")
}

// buildFromDef is the one domain-level compilation path shared by every
// surface format. BuildFromYAML/BuildFromYAMLFile (yaml.go) decode into
// the same WorkflowDef and call this too, so a hand-written YAML runbook
// and a JSON-built one produce the identical *DAG: there is exactly one
// runbook-to-DAG path, not two that can drift apart. baseDir is the
// directory an import_tasks task's relative file reference resolves
// against (see resolveImportTasks, import_tasks.go); an empty baseDir
// means no file context is available, which is fine unless def actually
// uses import_tasks.
func (b *Builder) buildFromDef(def WorkflowDef, baseDir string) (*DAG, error) {
	if err := resolveImportTasks(&def, baseDir); err != nil {
		return nil, err
	}
	// After imports, so an imported file's tasks inherit the check_mode and
	// tags of the import_tasks task that pulled them in.
	propagateCheckMode(&def)
	propagateTags(&def)

	switch def.Type {
	case "", "native":
		// Default. Proceed normally.
	case "ansible":
		return nil, fmt.Errorf("runbook declares type %q, which the native engine does not run; '%s <file>' converts a playbook to a native runbook, and %s also covers running one unchanged", def.Type, migrateCommand, migrationGuide)
	default:
		return nil, fmt.Errorf("unrecognized runbook type %q: expected \"native\" (the default) or \"ansible\"", def.Type)
	}

	if !validRunbookID.MatchString(def.ID) {
		return nil, fmt.Errorf("invalid runbook id %q: only letters, digits, hyphens, and underscores are allowed, since the id is embedded in NATS subject strings and a \".\", \"*\", or trailing \">\" would widen or misroute a subject beyond what the publisher intended", def.ID)
	}

	// Computed from def after resolveImportTasks has already expanded
	// every import_tasks task in place above, so Version reflects the
	// fully-resolved definition (see DAG.Version's own doc comment for
	// why that matters). encoding/json is already deterministic here:
	// struct fields marshal in fixed declaration order and map keys sort,
	// so no separate canonicalization step is needed. The rollback keys
	// are left out (forwardDefinition): the version names what the forward
	// run does.
	resolvedJSON, err := json.Marshal(forwardDefinition(def))
	if err != nil {
		return nil, fmt.Errorf("failed to compute content-hash version: %w", err)
	}
	versionSum := sha256.Sum256(resolvedJSON)
	version := "sha256:" + hex.EncodeToString(versionSum[:])

	dag := &DAG{
		ID:         def.ID,
		Name:       def.Name,
		Version:    version,
		Metadata:   def.Metadata,
		Hosts:      def.Hosts,
		CheckMode:  bool(def.CheckMode),
		Reversible: def.Reversible,
		PreTasks:   def.PreTasks,
		Tasks:      def.Tasks,
		PostTasks:  def.PostTasks,
		Nodes:      make(map[string]*Task),
		Adjacency:  make(map[string][]EdgeConfig),
		Conditions: make(map[string]*ConditionProgram),
	}

	// Every task is registered here, validated and compiled, whatever its
	// tags: a task a filter leaves out must still be a task that would
	// build (chain.go, then Select below).
	w := chainWalker{
		dag:      dag,
		register: func(task *Task, id string) error { return b.registerTask(dag, task, id) },
		include:  func(*Task) bool { return true },
	}
	if err := w.wire(def.PreTasks, def.Tasks, def.PostTasks); err != nil {
		return nil, err
	}

	// Cycle Detection (DFS). The graph is built entirely by this
	// deterministic walk over a tree, so a cycle should be structurally
	// unreachable; this check is cheap and stays as a defensive guard.
	if hasCycle(dag) {
		return nil, fmt.Errorf("circular dependency detected in workflow DAG")
	}

	// Ansible's default selection, --tags all, leaves out a task tagged
	// never. Applied here so every tier gets it without asking. A runbook
	// with no tags at all is unchanged by it, so it is not projected.
	dag.full = &fullGraph{nodes: dag.Nodes, conditions: dag.Conditions}
	if len(dag.TagNames()) == 0 {
		return dag, nil
	}
	return Select(dag, TagFilter{})
}

// dfsColor is hasCycle's own per-node visitation state: white (never
// pushed), gray (on the current DFS path, not yet fully explored), or
// black (fully explored, safe to skip on a later encounter).
type dfsColor int

const (
	dfsWhite dfsColor = iota
	dfsGray
	dfsBlack
)

// dfsFrame is one entry in hasCycle's explicit stack, standing in for a
// recursive dfs(nodeID) call's own stack frame: edgeIndex remembers which
// of nodeID's outgoing edges to resume from when this frame is revisited,
// exactly what a recursive call's local loop variable would otherwise
// track on the real call stack.
type dfsFrame struct {
	nodeID    string
	edgeIndex int
}

// hasCycle reports whether dag's Adjacency graph contains a cycle, via an
// iterative depth-first search over an explicit stack rather than
// recursion. A recursive DFS here would recurse once per edge along the
// longest path in the graph, and a large flat tasks: list (no nesting
// needed at all - synthesizeChain always chains a flat list into one long
// line) grows that path with every extra task, unbounded by
// maxTaskNestingDepth (import_tasks.go), which bounds nesting depth, not
// list length. Go's goroutine stacks grow dynamically rather than being a
// fixed size, so this is not a "one small payload instantly crashes the
// process" bug the way it might be in another language, but the ceiling
// (1GB by default, runtime/debug.SetMaxStack) is still finite and a fatal,
// unrecoverable crash once reached, not a catchable panic, and several
// concurrent requests each with a large-but-not-individually-fatal task
// list can exhaust process memory well before any one of them gets there
// alone. Recursion depth here should not scale with input size at all
// when a flat, iterative rewrite costs nothing extra (Schema/Injection
// Hardening finding, FAILURE_PATTERNS.md #62).
func hasCycle(dag *DAG) bool {
	color := make(map[string]dfsColor, len(dag.Nodes))

	for start := range dag.Nodes {
		if color[start] != dfsWhite {
			continue
		}

		stack := []dfsFrame{{nodeID: start}}
		color[start] = dfsGray

		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			edges := dag.Adjacency[top.nodeID]

			if top.edgeIndex >= len(edges) {
				// Every outgoing edge explored: this node is done.
				color[top.nodeID] = dfsBlack
				stack = stack[:len(stack)-1]
				continue
			}

			next := edges[top.edgeIndex].To
			top.edgeIndex++

			switch color[next] {
			case dfsWhite:
				color[next] = dfsGray
				stack = append(stack, dfsFrame{nodeID: next})
			case dfsGray:
				// next is still on the current path: a back-edge, i.e. a
				// cycle.
				return true
			case dfsBlack:
				// Already fully explored via another path; nothing to do.
			}
		}
	}

	return false
}
