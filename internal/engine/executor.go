package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/credential"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/google/uuid"
)

// defaultMaxConcurrency bounds how many device executions Executor.Run
// allows in flight at once when NewExecutor is given zero or a negative
// value. It matches ansible-playbook's own default forks value, so
// executor_bench_test.go's comparison against a real ansible-playbook run
// measures a genuinely comparable degree of parallelism, not an
// apples-to-oranges one.
const defaultMaxConcurrency = 5

// defaultLockTTL bounds how long Executor holds a device's lock before
// lock.Manager treats it as abandoned. It is a safety net, not the
// primary release mechanism: every successful Acquire is paired with a
// deferred Release, including one issued against a fresh background
// context when the run's own ctx is already canceled, mirroring
// Scheduler.Run's own graceful-handover idiom (scheduler.go: "Use a
// background context since the parent ctx is already dead"). It is
// generous because the Walk-tier ActionExecutor today only ever runs the
// near-instant "noop" action; a longer-running or per-task configurable
// value is Phase W6's real transport dispatch's concern, not this one's.
const defaultLockTTL = 5 * time.Minute

// nodeExecution is the Command object (PATTERNS.md's Command entry) for
// one task's execution against one resolved device, or against no device
// at all for a controller-side task (PLAN.md Section 14's Execution
// Contexts). It mirrors runner.DispatchPayload's role at whole-runbook
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

	// Changed reports whether the action reported altering real state.
	Changed bool

	// Err is non-nil if resolving the target, acquiring a lock, running
	// the action, or recording its result failed.
	Err error
}

// RunResult aggregates every NodeResult produced walking a DAG with
// Executor.Run, in the order each one finished, which is not necessarily
// TopologicalOrder's flat order: nodes within the same level, and devices
// within the same node's fan-out, run concurrently and can finish in any
// order.
type RunResult struct {
	Nodes []NodeResult

	// Secrets is every value a register_mask or secret_mask task
	// annotation discovered during this run (see Task.RegisterMask,
	// Task.SecretMask), in no particular order. A caller that prints or
	// logs this run's own output (cmd/pleiades/run.go) should mask through
	// credential.Mask using this exact, complete slice after Run has
	// returned. This is strictly more complete than publish's own
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
}

// NewExecutor builds an Executor from its dependencies (Dependency
// Injection, PATTERNS.md), matching every other constructor in this
// codebase. maxConcurrency bounds the total number of device executions
// in flight at once across the whole Run call, regardless of whether that
// concurrency comes from one node fanning out across many devices or
// several nodes in the same graph level running at once; a value of zero
// or less falls back to defaultMaxConcurrency.
func NewExecutor(resolver TargetResolver, actions ActionExecutor, locks lock.Manager, bus event.Bus, workflow WorkflowContext, maxConcurrency int) *Executor {
	if maxConcurrency <= 0 {
		maxConcurrency = defaultMaxConcurrency
	}
	return &Executor{
		resolver:       resolver,
		actions:        actions,
		locks:          locks,
		bus:            bus,
		workflow:       workflow,
		maxConcurrency: maxConcurrency,
	}
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
	x                 *Executor
	dag               *DAG
	sem               chan struct{}
	secrets           *stringSet
	metadataRegisters *stringSet
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
	r := &run{x: x, dag: dag, sem: make(chan struct{}, x.maxConcurrency), secrets: newStringSet(), metadataRegisters: newStringSet()}

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
	for {
		level, ok := it.Next()
		if !ok {
			break
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
			return
		}

		outs := runConcurrently(level, func(nodeID string) []NodeResult {
			return r.runNode(ctx, nodeID)
		})

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
		return []NodeResult{{NodeID: nodeID}}
	}

	if cp := r.dag.Conditions[nodeID]; cp != nil {
		tree, err := r.x.workflow.Read()
		if err != nil {
			return []NodeResult{{NodeID: nodeID, Err: fmt.Errorf("failed to read workflow context for %s: %w", taskLabel(nodeID, task), err)}}
		}
		// Both CEL roots are bound to the identical snapshot today: "stat"
		// for simple, non-cross-node conditions and "nodes" for Section
		// 27-style cross-node conditions (e.g. nodes.precheck[""].ok), a
		// deliberate scope choice recorded in cel.go's NewCELEvaluator doc
		// comment rather than a narrower, diverging "stat" meaning nothing
		// here needs yet.
		vars := map[string]interface{}{"stat": tree, "nodes": tree}
		res, err := cp.Eval(vars)
		if err != nil {
			return []NodeResult{{NodeID: nodeID, Err: fmt.Errorf("failed to evaluate condition for %s: %w", taskLabel(nodeID, task), err)}}
		}
		if !res.OK {
			r.publish(nodeID, task, "", "skipped", res.Reason)
			return []NodeResult{{NodeID: nodeID, Skipped: true, SkipReason: res.Reason}}
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
		return []NodeResult{{NodeID: nodeID, Err: wrapped}}
	}

	devices, err := r.resolveDevices(task)
	if err != nil {
		wrapped := fmt.Errorf("failed to resolve target for %s: %w", taskLabel(nodeID, task), err)
		r.publish(nodeID, task, "", "failed", wrapped.Error())
		return []NodeResult{{NodeID: nodeID, Err: wrapped}}
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
			if !d.State().CanExecute() {
				reason := fmt.Sprintf("device %q is %s, not active", d.Name(), d.State())
				r.publish(nodeID, task, d.Name(), "skipped", reason)
				results = append(results, NodeResult{NodeID: nodeID, Device: string(d.ID()), Skipped: true, SkipReason: reason})
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
			return append(results, NodeResult{NodeID: nodeID, Err: wrapped})
		}
		for i := range cmds {
			cmds[i].Lease = leases[i]
		}
	}

	return append(results, runConcurrently(cmds, func(cmd nodeExecution) NodeResult {
		return r.runOne(ctx, cmd)
	})...)
}

// resolveDevices resolves task's Params["target"], returning (nil, nil)
// for a controller-side task with no target at all. A non-empty target
// that resolves to no device is an error: capability_rule.go only checks
// target existence for an fqcn that requires a capability, so a target
// typo on an unconstrained fqcn (including "noop") would otherwise pass
// validation silently and then do nothing at all at execution time,
// exactly the kind of silent drop this codebase's own FAILURE_PATTERNS.md
// already tracks as a defect class elsewhere.
func (r *run) resolveDevices(task *Task) ([]inventory.InventoryItem, error) {
	target, _ := task.Params["target"].(string)
	if target == "" {
		return nil, nil
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

	result := NodeResult{NodeID: cmd.NodeID}
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
				result.Err = fmt.Errorf("failed to acquire lock on device %q: %w", host, err)
				r.publish(cmd.NodeID, cmd.Task, host, "failed", result.Err.Error())
				return result
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

	actionResult, err := r.x.actions.Execute(ctx, cmd.Task, cmd.Device)
	if err != nil {
		result.Err = fmt.Errorf("task %s failed: %w", taskLabel(cmd.NodeID, cmd.Task), err)
		r.publish(cmd.NodeID, cmd.Task, host, "failed", err.Error())
		return result
	}

	// register_mask marks fields of this task's own just-computed result as
	// secret, before Register/Merge below records it anywhere: this way a
	// masked value is unmasked in WorkflowContext (when_cel must always see
	// real values) but is already tracked for every later output boundary.
	if err := r.markRegisterMask(cmd, actionResult); err != nil {
		result.Err = fmt.Errorf("task %s failed: %w", taskLabel(cmd.NodeID, cmd.Task), err)
		r.publish(cmd.NodeID, cmd.Task, host, "failed", result.Err.Error())
		return result
	}

	if cmd.Task.Register != "" {
		if err := r.x.workflow.Merge(cmd.Task.Register, result.Device, actionResult.Stats); err != nil {
			result.Err = fmt.Errorf("failed to record result of task %s: %w", taskLabel(cmd.NodeID, cmd.Task), err)
			r.publish(cmd.NodeID, cmd.Task, host, "failed", result.Err.Error())
			return result
		}
		if actionResult.IsMetadata {
			r.metadataRegisters.Add(cmd.Task.Register)
		}
	}

	result.Changed = actionResult.Changed
	status := "ok"
	if actionResult.Changed {
		status = "changed"
	}
	r.publish(cmd.NodeID, cmd.Task, host, status, "")
	return result
}

// nodeEvent mirrors adapters/native.LogEvent's exact field shape
// (Timestamp, Status, Host, Task, EventData.Message), the vocabulary the
// Crawl-tier native Adapter already publishes to jobs.logs.<job-id>.
// Reusing the identical shape here, over the Walk-tier in-process Bus, is
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
		message = credential.Mask(r.secrets.Snapshot(), message)
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
