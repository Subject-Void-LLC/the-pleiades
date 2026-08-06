package engine

import (
	"encoding/json"
	"fmt"
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
}

// WorkflowDef represents a user runbook. The yaml and json tags agree
// field for field, so a hand-written YAML runbook and a JSON-built one
// decode into the identical Go value and therefore compile to the
// identical *DAG (Part 0 Phase W2's bidirectional-compilation requirement).
//
// Pleiades aims to be a strict superset of Ansible playbooks with a minimum
// barrier to entry, so a runbook is authored the way an Ansible playbook
// is: an ordered pretasks/tasks/posttasks list, not a hand-wired graph of
// nodes and edges. PreTasks and PostTasks give authors the same "setup,
// body, teardown" shape Ansible plays already use; Tasks is the only
// required list, matching Ansible where a play with no tasks: at all is
// unusual but pretasks/posttasks-only plays exist.
type WorkflowDef struct {
	ID string `json:"id" yaml:"id"`

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
	// dynamically issued token): see internal/credential.Mask, which this
	// reuses, for the "known, pre-registered secret" case this is
	// deliberately not. See executor_secrets.go for how these are collected
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
}

// DAG is the executable in-memory graph compiled from a WorkflowDef.
type DAG struct {
	ID       string
	Metadata Metadata

	// PreTasks, Tasks, and PostTasks are the original, unflattened task
	// lists this DAG was compiled from, preserved for callers that want
	// the authored tree shape rather than the flattened Nodes view.
	PreTasks  []Task
	Tasks     []Task
	PostTasks []Task

	// Nodes is the flattened view of every task in the runbook, including
	// every Block, Rescue, and Always descendant at any nesting depth,
	// keyed by its synthesized ID (see synthesizeChain and
	// collectSubtree). This is what validation and capability-checking
	// walk, since it covers every task, not just the ones on the happy
	// path.
	Nodes map[string]*Task

	// Adjacency holds the "happy path" chain only: PreTasks in order,
	// then Tasks in order, then PostTasks in order. Within a block task,
	// its Block children chain in order and splice into the position
	// their parent block task occupies, so the block task's own ID never
	// appears as a source or target in Adjacency. Rescue and Always
	// children are deliberately NOT part of this chain: "run this on
	// failure" cannot be expressed as a precondition, and there is no
	// executor yet to give it real meaning. They still get entries in
	// Nodes and Conditions, so validation and capability-checking still
	// cover them, just not here.
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
}

// EdgeConfig is a single synthesized structural connector in DAG.Adjacency.
// Synthesized edges are always unconditional: every conditional now lives
// on Task via the embedded Conditional and is compiled into
// DAG.Conditions, keyed by node ID, never on an edge, so EdgeConfig carries
// no condition of its own.
type EdgeConfig struct {
	To string
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
	normalized, err := normalizeWorkflowJSON(payload)
	if err != nil {
		return nil, err
	}

	var def WorkflowDef
	if err := json.Unmarshal(normalized, &def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
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

	switch def.Type {
	case "", "native":
		// Default. Proceed normally.
	case "ansible":
		return nil, fmt.Errorf("runbook declares type %q, which this engine cannot execute yet (no native Ansible execution path exists in this repository); see PLAN.md Section 23 for the planned interop path", def.Type)
	default:
		return nil, fmt.Errorf("unrecognized runbook type %q: expected \"native\" (the default) or \"ansible\"", def.Type)
	}

	if !validRunbookID.MatchString(def.ID) {
		return nil, fmt.Errorf("invalid runbook id %q: only letters, digits, hyphens, and underscores are allowed, since the id is embedded in NATS subject strings and a \".\", \"*\", or trailing \">\" would widen or misroute a subject beyond what the publisher intended", def.ID)
	}

	dag := &DAG{
		ID:         def.ID,
		Metadata:   def.Metadata,
		PreTasks:   def.PreTasks,
		Tasks:      def.Tasks,
		PostTasks:  def.PostTasks,
		Nodes:      make(map[string]*Task),
		Adjacency:  make(map[string][]EdgeConfig),
		Conditions: make(map[string]*ConditionProgram),
	}

	// Walk pretasks, tasks, and posttasks as three independent happy-path
	// chains, each producing the entry (first) and exit (last) node ID
	// reached within it. An empty list contributes no section at all
	// (entry == ""), so it is skipped when stitching the three chains
	// together below: a runbook missing pretasks or posttasks still
	// builds a correct chain rather than one with dangling empty edges.
	type section struct{ entry, exit string }
	var sections []section
	for _, list := range []struct {
		tasks  []Task
		prefix string
	}{
		{def.PreTasks, "pretasks"},
		{def.Tasks, "tasks"},
		{def.PostTasks, "posttasks"},
	} {
		entry, exit, err := b.synthesizeChain(dag, list.tasks, list.prefix)
		if err != nil {
			return nil, err
		}
		if entry != "" {
			sections = append(sections, section{entry: entry, exit: exit})
		}
	}

	// Stitch the (up to three) non-empty sections together in order: the
	// exit of one section connects to the entry of the next.
	for i := 1; i < len(sections); i++ {
		prev := sections[i-1]
		dag.Adjacency[prev.exit] = append(dag.Adjacency[prev.exit], EdgeConfig{To: sections[i].entry})
	}

	// The first non-empty section's entry is the whole chain's entry
	// point. If every one of PreTasks, Tasks, and PostTasks is empty,
	// sections is empty and EntryPoint stays "".
	if len(sections) > 0 {
		dag.EntryPoint = sections[0].entry
	}

	// Cycle Detection (DFS). The graph is built entirely by this
	// deterministic walk over a tree, so a cycle should be structurally
	// unreachable; this check is cheap and stays as a defensive guard.
	if hasCycle(dag) {
		return nil, fmt.Errorf("circular dependency detected in workflow DAG")
	}

	return dag, nil
}

// hasCycle performs a Depth-First Search to detect back-edges.
func hasCycle(dag *DAG) bool {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(nodeID string) bool
	dfs = func(nodeID string) bool {
		visited[nodeID] = true
		recStack[nodeID] = true

		for _, edge := range dag.Adjacency[nodeID] {
			if !visited[edge.To] {
				if dfs(edge.To) {
					return true
				}
			} else if recStack[edge.To] {
				// We hit a node currently in our recursion stack -> CYCLE!
				return true
			}
		}

		recStack[nodeID] = false
		return false
	}

	for nodeID := range dag.Nodes {
		if !visited[nodeID] {
			if dfs(nodeID) {
				return true
			}
		}
	}

	return false
}
