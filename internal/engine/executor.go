package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/google/uuid"
)

// DefaultMaxConcurrency bounds how many device executions Executor.Run
// allows in flight at once when NewExecutor is given zero or a negative
// value. It matches ansible-playbook's own default forks value, so
// executor_bench_test.go's comparison against a real ansible-playbook run
// measures a genuinely comparable degree of parallelism, not an
// apples-to-oranges one. Exported for `pleiades run --forks`, whose
// default it is.
const DefaultMaxConcurrency = 5

// defaultLockTTL bounds how long Executor holds a device's lock before
// lock.Manager treats it as abandoned. It is a safety net, not the
// primary release mechanism: every successful Acquire is paired with a
// deferred Release, including one issued against a fresh background
// context when the run's own ctx is already canceled, mirroring
// Scheduler.Run's own graceful-handover idiom (scheduler.go: "Use a
// background context since the parent ctx is already dead"). It is
// generous because the Crawl-tier ActionExecutor today only ever runs the
// near-instant "noop" action; a longer-running or per-task configurable
// value is Phase W6's real transport dispatch's concern, not this one's.
const defaultLockTTL = 5 * time.Minute

// nodeExecution is the Command object (PATTERNS.md's Command entry) for
// one task's execution against one resolved device, or against no device
// at all for a controller-side task (PLAN.md Section 14's Execution
// Contexts). It mirrors wire.DispatchPayload's role at whole-runbook
// granularity: a self-contained request value, decoupled from whatever
// dispatches it, so the exact same value could in principle be handed to
// a future durable queue consumer instead of a local goroutine without
// either side changing. That equivalence is this phase's own Adversarial
// Pattern Justification: in-process (this file) and distributed (Phase
// 14's Dispatcher/Agent pair) execution both reify "run this task,
// against this device" as a Command before running it; they differ only
// in what consumes the Command, a goroutine here versus a NATS consumer
// there, never in the shape of the request itself.
type nodeExecution struct {
	NodeID string
	Task   *Task
	Device inventory.InventoryItem // nil for a controller-side task

	// Lease, when non-nil, is a lock already acquired for Device before
	// runNode built this nodeExecution (AcquisitionAllAtPlanTime): runOne
	// uses it directly instead of calling lock.Manager.Acquire itself,
	// but still Releases it exactly as it would one it acquired itself.
	// Left nil for AcquisitionPerDeviceAsReached (the default), in which
	// case runOne acquires and releases its own lease as before.
	Lease lock.Lease
}

// NodeResult is one node's outcome against one device, or one
// controller-side invocation with no device at all.
type NodeResult struct {
	// NodeID is the synthesized graph ID (e.g. "tasks[0]") this result
	// belongs to, never the task's Register name.
	NodeID string

	// Device is the resolved device's ID this result ran against, or the
	// empty string for a controller-side task or a condition-skipped node.
	// A lifecycle-skipped node (SkipReason set) keeps Device populated: the
	// whole point of that skip is naming which device and state caused it,
	// so it must never look like the same empty-Device shape a
	// condition-skipped node has.
	Device string

	// Skipped reports whether this node/device never ran at all: either
	// the node's when/when_or/when_cel condition evaluated false, or (see
	// SkipReason) the resolved device's lifecycle state was not Active.
	Skipped bool

	// SkipReason names why Skipped is true. Always populated when Skipped
	// is true: a condition-based skip names the specific when/when_or/
	// when_cel expression responsible (ConditionResult.Reason, see
	// conditional.go), and a lifecycle skip names the device and its
	// actual state, so neither case is a silent, unexplained omission.
	SkipReason string

	// Unchecked reports that this node was reached in check mode and its
	// action could not be checked (an UncheckedError), or its condition
	// was left undecided there (check_conditions.go): it depends on a
	// result an unchecked task never registered, or failed on a field a
	// prediction may not carry. Skipped is always true alongside it, since
	// nothing ran, and SkipReason names the action and why.
	//
	// It is a field of its own rather than one more kind of skip because a
	// caller has to count these separately: a condition that evaluated
	// false is a finished answer, and an unchecked task is a gap in the
	// answer that the caller must not report as clean.
	Unchecked bool

	// Provider names the external Collection program behind this node's
	// method, with the digest it was loaded with, and is nil for a method
	// compiled into this binary. It travels with the result so a reader
	// far from the run can tell third-party work from Pleiades's own. It
	// comes from collection.Descriptor.Provider, which only the loader
	// sets.
	Provider *collection.Provider

	// Checked reports that this node ran in check mode, because the run
	// was a check or because its task carries check_mode: its Changed is
	// a prediction, not something that happened. In a real run with some
	// check_mode tasks it is the one field that tells the two kinds of
	// result apart.
	Checked bool

	// Changed reports whether the action reported altering real state. In
	// check mode it reports whether a real run WOULD alter it, since a
	// check alters nothing; Checked says which of the two a result means.
	Changed bool

	// Err is non-nil if resolving the target, acquiring a lock, running
	// the action, or recording its result failed.
	Err error

	// Stats is the action's own output (ActionResult.Stats): the stdout,
	// exit status, resolved paths and diffs a Collection method recorded
	// with sdk.RunbookContext.SetStat. Nil for a node that never ran.
	//
	// It is carried here in addition to being merged into WorkflowContext
	// under the task's Register name, because those two answer different
	// questions. WorkflowContext answers "what can a later task's when_cel
	// read", and is keyed by a register name a task without one does not
	// have. This answers "what did this node do", for a caller reporting
	// the run to a human, and a task's output should not become invisible
	// because nothing downstream needed it. Before this existed, a
	// successful task's output was unreachable from cmd/pleiades entirely
	// and the only way to read a device's answer back was to make the task
	// fail on purpose so the error path would print it.
	//
	// Values are unmasked, exactly as WorkflowContext holds them. Any
	// caller printing or logging them must mask through
	// RunResult.Secrets first.
	Stats map[string]interface{}

	// journalStats is what the run journal's projection reads instead of
	// Stats, because the two answer different questions and one of the two
	// post-action failure paths makes the difference load bearing.
	//
	// Stats is what a CALLER PRINTS: cmd/pleiades/run.go renders it under
	// --verbose. So it stays nil when markRegisterMask fails, and that is
	// deliberate rather than an oversight. A failed register_mask means the
	// author's own mask never applied, so publishing the value it was
	// written to protect is exactly the leak the annotation exists to
	// prevent.
	//
	// The journal has the opposite constraint and therefore needs its own
	// field. It never stores a value, only key NAMES (see projectResult and
	// admitStatKeys), so it can safely be shown stats a caller must not
	// print. And it MUST be shown them: a task that changed the device and
	// recorded an inverse, and only then failed at register_mask or at
	// record, is precisely the run this phase exists to journal. Reading
	// Stats there would make such a node journal as "nothing to undo",
	// which is worse than an absent entry because it is a confident wrong
	// answer to the one question a rollback asks.
	//
	// Unexported on purpose: nothing outside this package should reach a
	// value that deliberately bypasses the print path's own guard.
	journalStats map[string]interface{}

	// StartedAt and FinishedAt bound this one execution, both in UTC to
	// match publish's own clock. StartedAt is stamped once per node in
	// runNode for a result runNode produces itself, and once per device
	// in runOne after the semaphore has admitted that device, so the
	// interval measures execution rather than time spent queued behind
	// Executor.maxConcurrency.
	//
	// They exist because the run journal has to order a level's
	// concurrent fan-out, and the event stream cannot do it. publish
	// stamps its own payload with time.RFC3339 (see publish, below),
	// a layout carrying no fractional-second component at all, so two
	// nodes of the same level that finish three milliseconds apart carry
	// the identical instant and nothing downstream can tell which ran
	// first. A journal entry has to answer that, so it carries its own
	// bounds rather than inheriting the event's.
	//
	// Both are the zero Time on exactly one shape of result: the
	// synthetic parallel fan-out/join marker (TaskKindSynthetic), which
	// short-circuits ahead of the whole pipeline in runNode and executes
	// nothing at all. A zero pair there is the honest record of a node
	// that never ran, and it is deliberately not filled in with
	// time.Now(), which would fabricate an instant for work that did not
	// happen. Every other result sets both.
	StartedAt  time.Time
	FinishedAt time.Time

	// failureStage, skipKind, skipOrdinal and skipTotal are the run
	// journal's tags: the four things projectLevel (journal_entry.go)
	// cannot re-derive once a level has joined, recorded at the one call
	// site that knows each answer.
	//
	// They are unexported deliberately. They are the projection's private
	// channel, not a second public account of an outcome this type already
	// reports through Skipped, SkipReason and Err, and a caller outside
	// this package that wants them reads the journal rather than a field
	// whose meaning would then be frozen by everyone who found it.
	//
	// The tags exist at all because the alternative, recovering the stage
	// from Err's text, is both refused by the design (see
	// JournalEntry.FailureStage: a stage read off control flow cannot
	// drift when somebody rewords an error) and impossible here: runOne
	// wraps an action failure and a register_mask failure with the
	// identical "task %s failed:" prefix, so no rule over Err could tell
	// FailureStageAction from FailureStageRegisterMask.
	//
	// Leaving one unset is caught rather than absorbed. projectLevel
	// refuses to write an entry for a failed result carrying no stage, or
	// a skipped result carrying no kind, because a zero value there is a
	// record that quietly says the wrong thing. See projectLevel's own
	// comment on what a tenth failure site costs whoever adds it.
	failureStage FailureStage
	skipKind     SkipKind
	skipOrdinal  int
	skipTotal    int
}

// fail records err as n's failure and tags it with the stage that
// produced it, so the journal can say which step failed without reading
// the error's own text. It is a method rather than a field assignment at
// each site so the two always move together: a site that set Err alone
// would project as a failure with no stage and be refused.
func (n *NodeResult) fail(stage FailureStage, err error) {
	n.failureStage = stage
	n.Err = err
}

// failedNode returns a finished NodeResult for a node that failed at
// stage before it ever fanned out across devices, which is every failure
// runNode itself produces. runOne uses the fail method above instead,
// because it has a partly built result in hand by the time it fails.
func failedNode(nodeID string, started time.Time, stage FailureStage, err error) NodeResult {
	n := NodeResult{NodeID: nodeID, StartedAt: started}
	n.fail(stage, err)
	return finish(n)
}

// finish returns n with FinishedAt stamped as of now, in UTC.
//
// It exists so every place runNode and runOne return a result that
// actually executed shares one definition of "now", rather than a dozen
// separate time.Now() calls a later edit could leave out of one branch
// and produce a result with a start and no end.
//
// It deliberately does not stamp StartedAt as well. That instant means
// something different at the two producers (runNode takes one for the
// whole node, before it knows how many devices it will fan out across;
// runOne takes one per device, after the semaphore admits it), so it is
// passed in at the construction site where the difference is visible
// instead of being collapsed into this helper.
func finish(n NodeResult) NodeResult {
	n.FinishedAt = time.Now().UTC()
	return n
}

// RunResult aggregates every NodeResult produced walking a DAG with
// Executor.Run, in deterministic graph position: level by level, and
// within a level in the order runConcurrently was given its items, which
// is index-preserving (it writes results[i], it does not append as each
// goroutine finishes).
//
// This doc said "in the order each one finished" until this phase
// corrected it. Nodes within a level and devices within a node's fan-out
// genuinely do run concurrently and genuinely can finish in any order,
// which is presumably where the sentence came from, but none of that
// reaches the slice: the completion order is discarded by the join. A
// caller needing wall-clock order has NodeResult.StartedAt and
// FinishedAt, and a caller needing run order has JournalEntry.Sequence.
type RunResult struct {
	Nodes []NodeResult

	// Mode is the mode this run executed in (WithMode). It travels with
	// the result so a caller printing NodeResult.Changed can say "changed"
	// for an execute run and "would change" for a check, rather than
	// having to remember which one it asked for.
	Mode collection.Mode

	// Secrets is every value a register_mask or secret_mask task
	// annotation discovered during this run (see Task.RegisterMask,
	// Task.SecretMask), in no particular order. A caller that prints or
	// logs this run's own output (cmd/pleiades/run.go) should mask through
	// redact.Text using this exact, complete slice after Run has returned.
	// (It said credential.Mask until this phase corrected it; that
	// function was deleted in Phase 22, when the masking algorithm moved
	// to internal/redact.) This is strictly more complete than publish's own
	// best-effort, in-flight masking of each event's message as it is
	// published: Secrets reflects everything discovered by the time Run
	// returned, including a secret discovered only after an earlier event
	// already went out. Never written back into WorkflowContext: when_cel
	// must always see real, unmasked values, or branching logic silently
	// breaks.
	Secrets []string

	// Metadata holds every "set_metadata" task's registered result, keyed
	// by Task.Register, exactly the value WorkflowContext.Read returned
	// for that key (a map keyed by device ID, the empty string keying a
	// controller-side result). A runbook with no set_metadata task leaves
	// this nil. Populated once, after Run completes, from a single final
	// WorkflowContext.Read call, not accumulated per task.
	Metadata map[string]interface{}
}

// HasErrors reports whether any NodeResult in this run failed.
func (r RunResult) HasErrors() bool {
	for _, n := range r.Nodes {
		if n.Err != nil {
			return true
		}
	}
	return false
}

// Executor walks a compiled DAG in topological order (via LevelIterator)
// and runs every reachable node's action, fanning out across its resolved
// target devices. It evaluates each node's compiled when/when_or/when_cel
// condition against the current WorkflowContext before running it,
// acquires a per-device lock.Manager lease for the duration of each
// device's action (Section 13's lock granularity), publishes one
// lifecycle event per outcome to event.Bus, and merges a Register'd
// task's result back into WorkflowContext so a later node's condition can
// reference it. This is Executor's first real production use of
// lock.Manager and event.Bus, both built with no caller in Phase W4
// (HANDOFF_DOCUMENT.md's Phase W4 session): giving them one is this
// phase's own side effect on that phase's Release Gate.
//
// A node failure (a bad target, a lock contention failure, or an action
// error) stops the walk after the current level finishes, rather than
// continuing into a level that may have assumed the failed node's result
// was available; a Skipped node (condition evaluated false) is not a
// failure and never stops the walk. Block/rescue/always failure-recovery
// semantics are deliberately out of scope: DAG.Adjacency's own doc
// comment already notes rescue and always are excluded from the
// happy-path chain because "there is no executor yet to give it real
// meaning," and this phase's Release Gate is about a conditional edge
// taking the correct branch, not about failure recovery, so that stays a
// named, separate follow-up rather than being folded in silently.
type Executor struct {
	resolver       TargetResolver
	actions        ActionExecutor
	locks          lock.Manager
	bus            event.Bus
	workflow       WorkflowContext
	maxConcurrency int
	extraVars      map[string]interface{}
	taskTimeout    time.Duration
	journal        Journal

	// mode is collection.ModeExecute unless WithMode set it. See check.go
	// for what check mode does and refuses to do.
	mode collection.Mode

	// externalChecks is whether a check may run an external program's
	// Check (WithExternalChecks). Off by default.
	externalChecks bool
}

// ExecutorOption configures optional, non-default Executor behavior,
// mirroring internal/adapters/legacy.AdapterOption's own established
// shape in this codebase: every real dispatch path that has no reason to
// set one leaves it unset, and NewExecutor's existing six-argument call
// sites (cmd/pleiades/run.go, internal/adapters/native/adapter.go, and
// every engine test) keep compiling unchanged.
type ExecutorOption func(*Executor)

// WithVariables makes vars available to every when/when_or/when_cel
// condition this Executor evaluates, under CEL's "vars" root (cel.go's
// NewCELEvaluator), for the whole lifetime of a Run call. This is
// AWX_PARITY_ROADMAP.md Section 3b.1's own "ExtraVars folded into the
// runbook's variable context": a dispatch's resolved
// launch.Resolved.ExtraVars is the one thing this engine has ever had a
// reason to call a variable context at all (Task.Params carries no
// templating syntax; see internal/adapters/native/adapter.go's own doc
// comment on why this is the real, if narrow, hook). Unlike 'stat'/
// 'nodes', which WorkflowContext accumulates as tasks register results,
// vars is fixed for the whole run: the value supplied here, never mutated
// mid-run.
//
// A nil or never-supplied vars is not an error: runNode defaults to an
// empty map so 'vars' stays a valid CEL reference either way.
func WithVariables(vars map[string]interface{}) ExecutorOption {
	return func(x *Executor) { x.extraVars = vars }
}

// WithTaskTimeout bounds how long a single task's ActionExecutor.Execute
// call may run before its context is canceled, applied fresh to every
// node this Executor runs (runOne), not once for the whole Run call: the
// runbook kind's own FieldSpec help text is explicit that this field
// means "seconds before A TASK is abandoned," not "before the run is
// abandoned" (contrast internal/adapters/legacy's own runTimeout, which
// implements the playbook kind's whole-run reading of the identically
// named field, since ansible-playbook runs as one process per play with
// no per-task boundary this engine could reach into).
//
// Zero (the default, and NewExecutor's implicit prior behavior) applies
// no deadline at all: a launch that never set "timeout" must keep running
// exactly as long as its actions take, matching the field's own declared
// semantics ("zero means no timeout, which is the platform default
// rather than an omission").
func WithTaskTimeout(d time.Duration) ExecutorOption {
	return func(x *Executor) { x.taskTimeout = d }
}

// NewExecutor builds an Executor from its dependencies (Dependency
// Injection, PATTERNS.md), matching every other constructor in this
// codebase. maxConcurrency bounds the total number of device executions
// in flight at once across the whole Run call, regardless of whether that
// concurrency comes from one node fanning out across many devices or
// several nodes in the same graph level running at once; a value of zero
// or less falls back to DefaultMaxConcurrency. opts configures optional
// behavior (WithVariables, WithTaskTimeout, WithJournal); every existing
// call site that passes none keeps its prior OUTCOME and its prior event
// stream unchanged.
//
// Not literally its prior WORK, and the difference is worth stating
// rather than glossing. recordLevel runs at every level barrier whichever
// sink is installed, so a caller wiring none still pays one projectLevel
// pass (an entry per NodeResult, with two sorted key vectors each) and
// can still emit an error log line if the projection refuses a result.
// That is deliberate: the fail-closed refusal is only worth having if it
// fires in the default configuration too, and the benchmark in
// journal_bench_test.go measures exactly this cost.
//
// journal defaults to noopJournal rather than to nil, so the field is
// never nil and no caller has to opt out of a journal it never asked for
// (see WithJournal).
func NewExecutor(resolver TargetResolver, actions ActionExecutor, locks lock.Manager, bus event.Bus, workflow WorkflowContext, maxConcurrency int, opts ...ExecutorOption) *Executor {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	x := &Executor{
		resolver:       resolver,
		actions:        actions,
		locks:          locks,
		bus:            bus,
		workflow:       workflow,
		maxConcurrency: maxConcurrency,
		journal:        noopJournal{},
		mode:           collection.ModeExecute,
	}
	for _, opt := range opts {
		opt(x)
	}
	return x
}

// run holds the state scoped to a single Executor.Run call: the dag being
// walked, the worker-pool semaphore this call's device executions share,
// and the secret-tracking accumulators register_mask/secret_mask feed.
// Keeping this separate from Executor itself means Executor has no
// per-run mutable state, so a single Executor value stays safe to reuse
// (or even to call Run on concurrently) across more than one dag; a fresh
// secrets/metadataRegisters set is built for every call, so values from
// one run can never leak into a later Run call on a reused Executor.
type run struct {
	x   *Executor
	dag *DAG

	// runID identifies this one Run call in the run journal
	// (JournalEntry.RunID). It lives here rather than on Executor for the
	// same reason everything else in this type does: an identifier hung
	// off the Executor would be shared by every concurrent Run call on a
	// reused Executor instead of naming one of them.
	//
	// It is a version-4 UUID from github.com/google/uuid, and the source
	// is argued here rather than assumed, because the journal's whole
	// value rests on its identifiers meaning what they say:
	//
	//   - google/uuid is already a direct module dependency (go.mod's own
	//     require block, not an indirect one), and this exact file
	//     already calls uuid.New() once per published event (publish,
	//     below). So the run identifier adds no dependency, no import,
	//     and strictly less entropy draw than the code beside it. It is
	//     not a concrete driver, so TestEngineImportsNoConcreteDriver is
	//     unaffected: concreteDriverPrefixes names NATS, go-sqlite3,
	//     lib/pq and testcontainers-go, and no other archtest constrains
	//     what internal/engine may import.
	//   - crypto/rand on its own would mean inventing a length, an
	//     encoding and a format that every reader of a journal row then
	//     has to learn, in order to reach the same 122 random bits
	//     uuid.New() already reads from crypto/rand and prints in a shape
	//     an operator recognizes on sight.
	//   - A monotonic counter is the one candidate that is wrong rather
	//     than merely redundant. It needs state outliving a Run call,
	//     which is precisely the property this type exists to deny, and
	//     it collides across processes: on the Walk tier many Runner
	//     processes write into one journal, so "run 7" from two Runners
	//     would join into a single run that never happened. Grouping
	//     entries within an execution is this field's only job, and a
	//     grouping key that silently merges two executions is worse than
	//     having none.
	//
	// uuid.New panics if crypto/rand fails, which is the one real cost of
	// the choice. It is accepted rather than traded for uuid.NewRandom's
	// error return because publish already takes that identical risk on
	// every event this run will emit: a value minted once per run cannot
	// fail in a way the run would otherwise have survived.
	runID string

	sem               chan struct{}
	secrets           *stringSet
	metadataRegisters *stringSet

	// unknown is every registered result this run's check could not
	// produce (check_conditions.go), read by later tasks' conditions.
	unknown unknownRegisters

	// sequence numbers the journal entries this run has produced so far
	// (JournalEntry.Sequence), and journalFailures counts the Record calls
	// that failed.
	//
	// Neither needs a mutex, and the reason is structural rather than
	// hopeful: both are touched only by recordLevel, which Run calls from
	// its own goroutine at the level barrier, after runConcurrently's
	// wg.Wait has already joined every node goroutine of that level. No
	// device execution ever reaches them.
	sequence        int
	journalFailures int

	// mode is the run's own mode: the Executor's (WithMode), narrowed to
	// collection.ModeCheck when the runbook itself carries check_mode.
	// modeFor narrows it again per task.
	mode collection.Mode
}

// modeFor is the mode task runs in (TaskMode): a check whenever the run
// is one or the task carries check_mode, and the run's own mode
// otherwise. It can only narrow, so no task ever runs for real inside a
// check.
func (r *run) modeFor(task *Task) collection.Mode {
	return TaskMode(r.x.mode, r.dag, task)
}

// Run walks dag one topological level at a time (LevelIterator) and runs
// every reachable node, returning once every level has run, one level
// failed, or ctx is canceled. See Executor's own doc comment for the
// failure and skip semantics.
//
// The deferred cleanup below runs on every exit path, including the early
// ctx-canceled and LevelIterator-error returns, not just normal
// completion: a secret discovered before an abort must still be in
// result.Secrets so a caller can mask whatever partial output it produces.
func (x *Executor) Run(ctx context.Context, dag *DAG) (result RunResult, err error) {
	r := &run{
		x:                 x,
		dag:               dag,
		runID:             uuid.New().String(),
		sem:               make(chan struct{}, x.maxConcurrency),
		secrets:           newStringSet(),
		metadataRegisters: newStringSet(),
		// The runbook's own check_mode narrows a real run to a check.
		mode: TaskMode(x.mode, dag, nil),
	}
	result.Mode = r.mode

	defer func() {
		result.Secrets = r.secrets.Snapshot()
		if names := r.metadataRegisters.Snapshot(); len(names) > 0 {
			if stat, readErr := r.x.workflow.Read(); readErr == nil {
				result.Metadata = make(map[string]interface{}, len(names))
				for _, name := range names {
					if v, ok := stat[name]; ok {
						result.Metadata[name] = v
					}
				}
			}
		}
	}()

	it := NewLevelIterator(dag)
	// levelIndex is 1-based and exists only to name a level in a log line;
	// it is not stored in the journal, whose entries are ordered by
	// Sequence and grouped by RunID.
	levelIndex := 0
	for {
		level, ok := it.Next()
		if !ok {
			break
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
			return
		}
		levelIndex++

		outs := runConcurrently(level, func(nodeID string) []NodeResult {
			return r.runNode(ctx, nodeID)
		})

		// The journal is written here, before the failure scan below can
		// break out of the loop, because the level that failed is the one
		// its record is read for afterward. recordLevel never returns an
		// error and never changes this run's outcome; see its own doc
		// comment for why an audit write must not become an execution
		// failure on the Walk tier.
		r.recordLevel(ctx, levelIndex, outs)

		failed := false
		for _, out := range outs {
			result.Nodes = append(result.Nodes, out...)
			for _, nr := range out {
				if nr.Err != nil {
					failed = true
				}
			}
		}
		if failed {
			break
		}
	}

	if it.Err() != nil {
		err = it.Err()
		return
	}
	return
}

// runConcurrently runs fn once per item in items, each on its own
// goroutine, and returns every result in the same order as items. It is
// the shared Fan-Out/Fan-In helper (PATTERNS.md's Fan-Out/Fan-In entry)
// both the per-level node dispatch (Run) and the per-node device dispatch
// (runNode) build on, so the goroutine-launch-and-collect boilerplate
// exists in exactly one place. Boundedness comes from run.sem inside
// runOne, not from here: this helper always launches len(items)
// goroutines, but they block on the shared semaphore rather than all
// running their action at once.
func runConcurrently[T any, R any](items []T, fn func(T) R) []R {
	results := make([]R, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(i int, item T) {
			defer wg.Done()
			results[i] = fn(item)
		}(i, item)
	}
	wg.Wait()
	return results
}

// runNode evaluates nodeID's condition (if any), resolves its target
// devices, and fans out across them (or, for a controller-side task, runs
// once with no device), returning one NodeResult per device.
func (r *run) runNode(ctx context.Context, nodeID string) []NodeResult {
	task := r.dag.Nodes[nodeID]

	if task.Kind() == TaskKindSynthetic {
		// A Parallel task's own fan-out/join marker (tasktree.go's
		// synthesizeParallel): a bare structural node with no condition,
		// target, register, or action of its own. Running it through the
		// full pipeline below would acquire a pointless lock and publish
		// a spammy event for a node that carries no runbook author's
		// intent at all, so it short-circuits here instead. This is the
		// only Executor change Phase 10 makes; see EdgeType's own doc
		// comment (dag.go) for what deliberately stays out of scope.
		//
		// This is the one result in this file that leaves StartedAt and
		// FinishedAt zero, and it does so on purpose. The short-circuit
		// is above the clock read below, so there is no interval to
		// report: this node executed nothing. Stamping it with time.Now()
		// would give the journal a plausible instant for work that never
		// happened, which is the failure the pair is least able to
		// survive, since an operator reading a duration cannot tell a
		// fabricated one from a real one. The journal has its own name
		// for this shape (OutcomeNotReached, journal.go) rather than a
		// default, for the same reason.
		return []NodeResult{{NodeID: nodeID}}
	}

	// Everything below this line executes something, however briefly:
	// reading the workflow context, evaluating a condition, applying
	// secret_mask, resolving a target, acquiring locks. One clock read
	// bounds the whole node's own pipeline, so every result runNode
	// produces itself shares a start and reports its own end through
	// finish. runOne stamps its own start per device instead, because a
	// device that waited on the semaphore did not begin executing when
	// the node did.
	started := time.Now().UTC()

	if cp := r.dag.Conditions[nodeID]; cp != nil {
		tree, err := r.x.workflow.Read()
		if err != nil {
			return []NodeResult{failedNode(nodeID, started, FailureStageWorkflowRead,
				fmt.Errorf("failed to read workflow context for %s: %w", taskLabel(nodeID, task), err))}
		}
		// "stat" and "nodes" are bound to the identical WorkflowContext
		// snapshot today: "stat" for simple, non-cross-node conditions and
		// "nodes" for Section 27-style cross-node conditions (e.g.
		// nodes.precheck[""].ok), a deliberate scope choice recorded in
		// cel.go's NewCELEvaluator doc comment rather than a narrower,
		// diverging "stat" meaning nothing here needs yet. "vars" is
		// different: it is r.x.extraVars (WithVariables), fixed for the
		// whole Run call rather than grown by WorkflowContext, defaulting
		// to an empty map so a condition can reference vars.foo on an
		// Executor no caller supplied one for.
		extraVars := r.x.extraVars
		if extraVars == nil {
			extraVars = map[string]interface{}{}
		}
		condVars := map[string]interface{}{"stat": tree, "nodes": tree, "vars": extraVars}
		var res ConditionResult
		if r.modeFor(task) == collection.ModeCheck {
			// A condition the unchecked tasks before it leave undecided is
			// a gap in the check, not a failed run: it is named, nothing
			// is registered for it, and the walk carries on. One they do
			// not decide is answered, and one that is wrong fails as the
			// real run would (check_conditions.go).
			var undecided string
			res, undecided, err = r.checkCondition(task, cp, condVars, tree)
			if err == nil && undecided != "" {
				r.markUnchecked(task, "", true)
				r.publish(nodeID, task, "", "skipped", "could not check: "+undecided)
				return []NodeResult{finish(NodeResult{
					NodeID: nodeID, StartedAt: started, Skipped: true, Unchecked: true, Checked: true, SkipReason: undecided,
				})}
			}
		} else {
			res, err = cp.Eval(condVars)
		}
		if err != nil {
			return []NodeResult{failedNode(nodeID, started, FailureStageConditionEval,
				fmt.Errorf("failed to evaluate condition for %s: %w", taskLabel(nodeID, task), err))}
		}
		if !res.OK {
			r.publish(nodeID, task, "", "skipped", res.Reason)
			// The journal's tags travel with the result: which keyword the
			// author actually wrote, and the two numbers evalAnd and evalOr
			// already computed to build res.Reason. Taking them here rather
			// than re-deriving them at the level barrier is what keeps the
			// journal's numbers and the reason sentence from drifting apart.
			return []NodeResult{finish(NodeResult{
				NodeID: nodeID, StartedAt: started, Skipped: true, SkipReason: res.Reason,
				skipKind: skipKindFor(cp), skipOrdinal: res.Ordinal, skipTotal: res.Total,
			})}
		}
	}

	// secret_mask runs once per node, before device resolution: it does not
	// depend on which device this task itself targets, it reaches back to
	// an earlier register's data across every device that register has
	// (executor_secrets.go's applySecretMask). Running it here also means a
	// skipped node (handled above) never applies it, matching how
	// Register/Merge already never fires for a skipped node.
	if err := r.applySecretMask(task); err != nil {
		wrapped := fmt.Errorf("failed to apply secret_mask for %s: %w", taskLabel(nodeID, task), err)
		r.publish(nodeID, task, "", "failed", wrapped.Error())
		return []NodeResult{failedNode(nodeID, started, FailureStageSecretMask, wrapped)}
	}

	devices, err := r.resolveDevices(task)
	if err != nil {
		wrapped := fmt.Errorf("failed to resolve target for %s: %w", taskLabel(nodeID, task), err)
		r.publish(nodeID, task, "", "failed", wrapped.Error())
		return []NodeResult{failedNode(nodeID, started, FailureStageResolveTarget, wrapped)}
	}

	// The runtime half of the chain audit's lifecycle finding
	// (IMPLEMENTATION.md Phase W5): a device that resolveDevices found is
	// not necessarily one real work may target. Partitioning here, not
	// filtering inside resolveDevices, is deliberate: resolveDevices
	// already treats an empty result as a hard error ("matches no
	// inventory host or tag"), so filtering there would turn "matched
	// only non-Active devices" into that same confusing, wrong error
	// instead of the accurate skip this produces. validate.LifecycleRule
	// owns the matching plan-time check; this is required in addition to
	// it, for the same defense-in-depth reason runOne's ActionExecutor
	// re-checks HasCapability after CapabilityRule already did: a
	// dispatch path that never called Validate must still fail closed.
	var results []NodeResult
	cmds := make([]nodeExecution, 0, len(devices))
	if len(devices) == 0 {
		cmds = append(cmds, nodeExecution{NodeID: nodeID, Task: task})
	} else {
		for _, d := range devices {
			// LifecycleAdmits (admission.go) is this exact check, relocated
			// so a future non-Executor caller can reuse it verbatim instead
			// of re-deriving the identical reason wording. LifecycleAdmitsIn
			// wraps it with check mode's one exception, a simulate-locked
			// device (check.go).
			if ok, reason := LifecycleAdmitsIn(r.modeFor(task), d); !ok {
				r.publish(nodeID, task, d.Name(), "skipped", reason)
				// The node's own start, not a per-device one: this device
				// never became a nodeExecution and never reached runOne,
				// so the only interval that exists is the node's walk down
				// to this decision.
				results = append(results, finish(NodeResult{
					NodeID: nodeID, Device: string(d.ID()), StartedAt: started,
					Skipped: true, SkipReason: reason, skipKind: SkipKindLifecycle,
				}))
				continue
			}
			cmds = append(cmds, nodeExecution{NodeID: nodeID, Task: task, Device: d})
		}
	}

	// AcquisitionAllAtPlanTime: acquire every remaining cmd's device lock
	// up front, all-or-nothing, before any of them run. A lifecycle-skipped
	// device (handled above) was never added to cmds, so it never needs a
	// lock; a controller-side task (len(devices) == 0) has no Device to
	// lock either. On failure, no cmd for this node ever runs at all,
	// proving the all-or-nothing property for real rather than only
	// declaring it.
	if task.LockAcquisition == AcquisitionAllAtPlanTime && len(devices) > 0 {
		itemIDs := make([]string, len(cmds))
		for i, cmd := range cmds {
			itemIDs[i] = string(cmd.Device.ID())
		}
		leases, err := lock.AcquireAll(ctx, r.x.locks, itemIDs, defaultLockTTL, lock.AcquireOptions{})
		if err != nil {
			wrapped := fmt.Errorf("failed to acquire all locks up front for %s: %w", taskLabel(nodeID, task), err)
			r.publish(nodeID, task, "", "failed", wrapped.Error())
			return append(results, failedNode(nodeID, started, FailureStageLockAll, wrapped))
		}
		for i := range cmds {
			cmds[i].Lease = leases[i]
		}
	}

	return append(results, runConcurrently(cmds, func(cmd nodeExecution) NodeResult {
		return r.runOne(ctx, cmd)
	})...)
}

// resolveDevices resolves task's effective target (TaskTarget: task's own
// Params["target"], falling back to r.dag.Hosts), returning (nil, nil) for
// a controller-side task that has no target at all, from either source,
// and whose resolver carries no ambient default device either (see below).
// A non-empty target that resolves to no device is an error:
// capability_rule.go
// only checks target existence for an fqcn that requires a capability, so a
// target typo on an unconstrained fqcn (including "noop") would otherwise
// pass validation silently and then do nothing at all at execution time,
// exactly the kind of silent drop this codebase's own FAILURE_PATTERNS.md
// already tracks as a defect class elsewhere.
func (r *run) resolveDevices(task *Task) ([]inventory.InventoryItem, error) {
	target := TaskTarget(r.dag, task)
	if target == "" {
		// Consult the resolver even with no target, rather than declaring
		// the task controller-side outright. A resolver may carry an
		// ambient default device, and the Runner mesh's own
		// singleDeviceResolver (internal/adapters/native) does: a
		// dispatched runbook always runs against exactly the one device
		// its wire.DispatchPayload names, chosen Controller-side from the
		// dispatch request's own group rather than from the runbook's
		// hosts: key, so a mesh-dispatched runbook legitimately carries no
		// hosts: at all. A resolver with no such default returns nothing
		// here and the task stays controller-side exactly as before: Crawl
		// tier's validate.WorldView matches an empty target against no
		// Name and no Tag, so its behavior is unchanged by this branch.
		// An empty result here is deliberately not the error the non-empty
		// branch below raises, because "this task names no target" and "a
		// named target matches nothing" are different conditions and only
		// the second one is a mistake.
		return r.x.resolver.Resolve(""), nil
	}

	devices := r.x.resolver.Resolve(target)
	if len(devices) == 0 {
		return nil, fmt.Errorf("target %q matches no inventory host or tag", target)
	}
	return devices, nil
}

// runOne acquires cmd.Device's lock (if it has one), runs the action,
// merges a Register'd result into WorkflowContext, and publishes exactly
// one lifecycle event for the outcome. It blocks on r.sem first, so the
// number of runOne calls actually executing their action at once, across
// the whole Run call, never exceeds Executor.maxConcurrency.
func (r *run) runOne(ctx context.Context, cmd nodeExecution) NodeResult {
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	// StartedAt is read here, below the semaphore that has just admitted
	// this device, rather than at the top of the function. Time spent
	// blocked on r.sem is queueing behind Executor.maxConcurrency, not
	// execution, and folding it into the interval would make a device
	// that waited look slow when it was only late to start.
	//
	// FinishedAt is stamped through finish at each of the five returns
	// below, and deliberately not in a deferred closure over a named
	// return. A deferred stamp would run after the lock Release deferred
	// a few lines down, charging that cleanup to the task's own duration.
	result := NodeResult{NodeID: cmd.NodeID, StartedAt: time.Now().UTC(), Provider: externalProvider(cmd.Task.FQCN)}
	host := ""
	if cmd.Device != nil {
		host = cmd.Device.Name()
		result.Device = string(cmd.Device.ID())

		// AcquisitionAllAtPlanTime already acquired this device's lease in
		// runNode, before any cmd in this node started running; only
		// AcquisitionPerDeviceAsReached (the default) acquires here,
		// immediately before this one device's own action.
		lease := cmd.Lease
		if lease == nil {
			var err error
			lease, err = r.x.locks.Acquire(ctx, result.Device, defaultLockTTL, lock.AcquireOptions{})
			if err != nil {
				result.fail(FailureStageLockDevice, fmt.Errorf("failed to acquire lock on device %q: %w", host, err))
				r.publish(cmd.NodeID, cmd.Task, host, "failed", result.Err.Error())
				return finish(result)
			}
		}
		defer func() {
			// ctx may already be canceled by the time we get here (a
			// sibling device's failure aborted the run); Release still
			// needs to run so the lock is not held until its TTL expires,
			// so it goes against a fresh background context, exactly the
			// way Scheduler.Run releases its own lease on shutdown.
			_ = lease.Release(context.Background())
		}()
	}

	// WithTaskTimeout applies fresh to every task, not once for the whole
	// Run call: a slow task earlier in the DAG must not shorten how long a
	// later, independent task is allowed to run. Zero (the default) wraps
	// nothing, leaving ctx exactly as the caller supplied it.
	execCtx := ctx
	if r.x.taskTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, r.x.taskTimeout)
		defer cancel()
	}

	var actionResult ActionResult
	var err error
	mode := r.modeFor(cmd.Task)
	result.Checked = mode == collection.ModeCheck
	switch mode {
	case collection.ModeExecute:
		actionResult, err = r.x.actions.Execute(execCtx, cmd.Task, cmd.Device)
		if err == nil {
			err = refusePrediction(cmd.Task.FQCN, actionResult.Stats)
		}
	case collection.ModeCheck:
		actionResult, err = r.checkAction(execCtx, cmd)
		// An action that cannot be checked is reported by name and the
		// walk carries on (check.go's second rule). It is not a failure,
		// so it gets no failure stage, and nothing is registered for it:
		// a later condition reading its result is handled in runNode.
		var unchecked *UncheckedError
		if errors.As(err, &unchecked) {
			r.markUnchecked(cmd.Task, result.Device, false)
			result.Skipped = true
			result.Unchecked = true
			result.SkipReason = unchecked.Error()
			r.publish(cmd.NodeID, cmd.Task, host, "skipped", "could not check: "+unchecked.Reason)
			return finish(result)
		}
	default:
		// Never treated as execute: see WithMode.
		err = fmt.Errorf("unknown execution mode %q, so nothing was run", mode)
	}
	if err != nil {
		result.fail(FailureStageAction, fmt.Errorf("task %s failed: %w", taskLabel(cmd.NodeID, cmd.Task), err))
		r.publish(cmd.NodeID, cmd.Task, host, "failed", err.Error())
		return finish(result)
	}

	// The action succeeded, so its stats exist and the journal is entitled
	// to their key names from here on, whatever happens next. This is set
	// once, above both post-action failure returns, rather than beside the
	// exported Stats assignment at the bottom: the two returns below carry
	// a fully populated actionResult, and a node that changed the device
	// and then failed at register_mask or record is the exact run the
	// journal exists to record. See NodeResult.journalStats for why this
	// is a separate field from Stats rather than a widening of it.
	result.journalStats = actionResult.Stats

	// register_mask marks fields of this task's own just-computed result as
	// secret, before Register/Merge below records it anywhere: this way a
	// masked value is unmasked in WorkflowContext (when_cel must always see
	// real values) but is already tracked for every later output boundary.
	if err := r.markRegisterMask(cmd, actionResult); err != nil {
		result.fail(FailureStageRegisterMask, fmt.Errorf("task %s failed: %w", taskLabel(cmd.NodeID, cmd.Task), err))
		r.publish(cmd.NodeID, cmd.Task, host, "failed", result.Err.Error())
		return finish(result)
	}

	if cmd.Task.Register != "" {
		if err := r.x.workflow.Merge(cmd.Task.Register, result.Device, actionResult.Stats); err != nil {
			result.fail(FailureStageRecord, fmt.Errorf("failed to record result of task %s: %w", taskLabel(cmd.NodeID, cmd.Task), err))
			r.publish(cmd.NodeID, cmd.Task, host, "failed", result.Err.Error())
			return finish(result)
		}
		if actionResult.IsMetadata {
			r.metadataRegisters.Add(cmd.Task.Register)
		}
	}

	result.Changed = actionResult.Changed
	result.Stats = actionResult.Stats
	status := "ok"
	if actionResult.Changed {
		status = "changed"
	}
	r.publish(cmd.NodeID, cmd.Task, host, status, "")
	return finish(result)
}

// nodeEvent mirrors adapters/native.LogEvent's exact field shape
// (Timestamp, Status, Host, Task, EventData.Message), the vocabulary the
// Walk-tier native Adapter already publishes to jobs.logs.<job-id>.
// Reusing the identical shape here, over the Crawl-tier in-process Bus, is
// the concrete evidence for this phase's Adversarial Pattern
// Justification: in-process and distributed execution report the same
// event contract, they just publish it through different Bus adapters and
// under a different subject. Unlike the native Adapter, which calls
// jetstream.JetStream.PublishMsg directly with a bare LogEvent payload,
// this file goes through the actual event.Bus port, so nodeEvent travels
// as the Data payload of a real event.Event envelope (see publish),
// matching Bus.Publish's own documented contract ("the payload is
// expected to be a marshaled JSON string of the Event struct").
type nodeEvent struct {
	Timestamp string `json:"timestamp"`
	Status    string `json:"status"`
	Host      string `json:"host"`
	Task      string `json:"task"`
	EventData struct {
		Message string `json:"message"`
	} `json:"event_data"`
}

// publish wraps a nodeEvent describing status in an event.Event envelope
// (event.WrapPayload) and fires it to
// "pleiades.events.workflow.<dag.ID>.node.<nodeID>", the subject space
// event.NewNatsBus's own stream is actually configured for
// ("pleiades.events.>"), unlike adapters/native.Adapter's own
// "jobs.logs.<job-id>" subject, which does not match that stream at all
// (see FAILURE_PATTERNS.md's entry recording that pre-existing,
// out-of-scope mismatch). Publish errors are deliberately ignored: event
// publication is an observability side effect, matching Bus.Publish's own
// fire-and-forget contract, and must never fail a node's real outcome.
//
// message is masked through every register_mask/secret_mask value known to
// r.secrets as of this exact call, before it ever reaches the payload.
// This is necessarily best-effort, not complete: an event published before
// a later task marks something secret cannot be retroactively scrubbed.
// RunResult.Secrets, populated only once Run returns, is the complete set;
// a caller masking its own printed output with that slice catches what
// this best-effort pass cannot.
func (r *run) publish(nodeID string, task *Task, host, status, message string) {
	label := task.Name
	if label == "" {
		label = task.FQCN
	}
	if message != "" {
		message = redact.Text(r.secrets.Snapshot(), message)
	}

	var payload nodeEvent
	payload.Timestamp = time.Now().UTC().Format(time.RFC3339)
	payload.Status = status
	payload.Host = host
	payload.Task = label
	payload.EventData.Message = message

	ev, err := event.WrapPayload(uuid.New().String(), "workflow.node.status", payload)
	if err != nil {
		return
	}

	topic := fmt.Sprintf("pleiades.events.workflow.%s.node.%s", r.dag.ID, nodeID)
	_ = r.x.bus.Publish(context.Background(), topic, *ev)
}
