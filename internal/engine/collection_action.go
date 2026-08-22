package engine

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// collectionActionExecutor runs a task whose FQCN names a registered
// Collection method, delegating anything it does not recognize to a
// fallback executor.
//
// This is the bridge that was missing. Before it, pkg/collection was
// planning-time metadata only: the Forge generated 71 method packages, each
// registering a real Go function, and no execution path anywhere could call
// one. The only executor was builtinActionExecutor's hardcoded switch over
// two literal FQCN strings, so a runbook calling pkg.apt.install failed with
// "not implemented" no matter how implemented that method actually was.
//
// It composes rather than replaces. noop and set_metadata are engine
// keywords rather than Collection methods (they are not namespaced and
// never appear in the registry), so they keep working through the fallback,
// and this type never needs to know they exist.
type collectionActionExecutor struct {
	// fallback handles any FQCN not registered as a Collection method.
	fallback ActionExecutor

	// newContext builds the sdk.RunbookContext a method is handed. It is a
	// field rather than a hardcoded constructor so a caller can supply one
	// that captures emitted facts and resolves the device's secrets, which
	// is what a real run needs and what tests assert against.
	newContext RunbookContextFunc

	// invoke, when non-nil, replaces how a resolved, StatusImplemented
	// method's body actually runs; see CollectionInvoker's own doc comment.
	invoke CollectionInvoker
}

// CollectionInvoker replaces how a registered, StatusImplemented
// Collection method's body actually runs, once collectionActionExecutor
// has already resolved and status-checked it via desc. device and params
// are exactly what desc.Invoke itself would receive.
//
// This is the Decorator Phase 16 (Native Go Execution Adapter)'s own
// Pattern Entry Gate names: it decorates HOW a method runs (a per-task
// subprocess boundary, PLAN.md Section 17.5) without duplicating or
// bypassing collectionActionExecutor's own dispatch, status-check, or
// fact-collection logic. Nil (the default: WithCollectionInvoker is never
// called) means "call desc.Invoke directly, in-process," today's exact,
// unchanged Crawl-tier behavior -- cmd/pleiades/run.go's own
// NewCollectionActionExecutor call needs no change at all.
type CollectionInvoker func(ctx context.Context, desc collection.Descriptor, device inventory.InventoryItem, params map[string]interface{}) (collection.Result, map[string]interface{}, error)

// CollectionActionExecutorOption configures optional, non-default behavior
// on a collectionActionExecutor built by NewCollectionActionExecutor.
type CollectionActionExecutorOption func(*collectionActionExecutor)

// WithCollectionInvoker installs invoke as the CollectionInvoker a
// collectionActionExecutor uses in place of calling desc.Invoke directly.
func WithCollectionInvoker(invoke CollectionInvoker) CollectionActionExecutorOption {
	return func(e *collectionActionExecutor) {
		e.invoke = invoke
	}
}

// NewCollectionActionExecutor returns an ActionExecutor that dispatches
// registered Collection methods and delegates everything else to fallback.
//
// Pass NewBuiltinActionExecutor() as fallback to keep the engine keywords
// working, which is what the composition root does.
func NewCollectionActionExecutor(fallback ActionExecutor, newContext RunbookContextFunc, opts ...CollectionActionExecutorOption) ActionExecutor {
	e := &collectionActionExecutor{fallback: fallback, newContext: newContext}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// checkMethodCapabilities refuses a Collection method whose target device
// does not carry every capability the method's manifest requires.
//
// Until this existed, Manifest.RequiredCapabilities was documentation.
// pkg/collection.Register validates that each name is a capability this
// vocabulary knows, and tools/gendocs prints the list on the reference
// page, but nothing anywhere compared it against the device a task was
// about to run on. A method declaring SystemdCapable would happily
// invoke against a Cisco switch, and the first sign of trouble would be
// whatever the remote shell said about "systemctl".
//
// It deliberately uses HasCapability rather than comparing declared
// names directly, for two reasons that matter here. HasCapability
// resolves the hierarchy (record.Base.Declares runs capability.Resolves),
// so a device declaring the concrete SystemdCapable satisfies a method
// requiring the broad ServiceManagerCapable, which is exactly what
// ServiceManagerCapable's own doc comment says the parent exists for. And
// on a real device type it also runs the structural assertion, so a
// declaration the Go type cannot back does not pass.
//
// This mirrors, at run time, what validate.CapabilityRule already does at
// plan time for transport-backed fqcns. Both exist for the same reason
// the transport executor keeps its own check after validation has run: a
// plan can be built, stored, and executed later against a registry or an
// inventory that has since changed.
func checkMethodCapabilities(desc collection.Descriptor, fqcn string, device inventory.InventoryItem) error {
	if len(desc.Manifest.RequiredCapabilities) == 0 {
		return nil
	}
	if device == nil {
		return fmt.Errorf("collection method %q requires capabilities %v but the task has no target device",
			fqcn, desc.Manifest.RequiredCapabilities)
	}
	for _, required := range desc.Manifest.RequiredCapabilities {
		if !device.HasCapability(required) {
			return fmt.Errorf("collection method %q requires capability %s, which device %q does not have",
				fqcn, required, device.Name())
		}
	}
	return nil
}

// Execute runs task, dispatching to the registered Collection method when
// one exists.
func (e *collectionActionExecutor) Execute(ctx context.Context, task *Task, device inventory.InventoryItem) (ActionResult, error) {
	desc, ok := collection.Lookup(task.FQCN)
	if !ok {
		return e.fallback.Execute(ctx, task, device)
	}

	// A declared method is a stub by definition. Refusing here rather than
	// calling it is the run-time half of the guardrail internal/validate's
	// CollectionRule already applies at plan time: validate catches this
	// when a runbook is written, and this catches a registry that changed
	// underneath a plan that was already built.
	if desc.Manifest.Status != collection.StatusImplemented {
		return ActionResult{}, fmt.Errorf("collection method %q is declared but not implemented", task.FQCN)
	}
	if desc.Invoke == nil {
		// Register rejects this combination, so reaching it means something
		// bypassed Register. Refusing beats a nil-pointer panic.
		return ActionResult{}, fmt.Errorf("collection method %q is registered as implemented but carries no implementation", task.FQCN)
	}

	if err := checkMethodCapabilities(desc, task.FQCN, device); err != nil {
		return ActionResult{}, err
	}

	if e.invoke != nil {
		result, stats, err := e.invoke(ctx, desc, device, task.Params)
		if err != nil {
			return ActionResult{}, fmt.Errorf("collection method %q: %w", task.FQCN, err)
		}
		return ActionResult{Changed: result.Changed, Stats: stats}, nil
	}

	// Building the context is where a device's secrets are resolved, so a
	// failure here is a real one (an unreadable credential store, a wrong
	// master key) and is reported rather than degraded into an empty
	// secret set. A device that simply has no stored credential is not a
	// failure and never reaches this branch; see NewCredentialRunbookContext.
	rc, err := e.newContext(ctx, device)
	if err != nil {
		return ActionResult{}, fmt.Errorf("collection method %q: %w", task.FQCN, err)
	}

	result, err := desc.Invoke(ctx, rc, device, task.Params)
	if err != nil {
		return ActionResult{}, fmt.Errorf("collection method %q: %w", task.FQCN, err)
	}

	stats := map[string]interface{}{}
	if collector, isCollector := rc.(FactCollector); isCollector {
		stats = collector.Facts()
	}

	return ActionResult{Changed: result.Changed, Stats: stats}, nil
}

// FactCollector is satisfied by a RunbookContext that accumulates the facts
// a method emitted, so the executor can surface them as an ActionResult's
// Stats and a later task's when_cel can read them.
//
// It is a separate interface rather than a method on sdk.RunbookContext
// because the SDK contract describes what a Collection author may call, and
// "give me back everything that was emitted" is not something an author
// calls. It is something the engine asks of the context it built.
type FactCollector interface {
	// Facts returns everything EmitFact and SetStat recorded, keyed by name.
	Facts() map[string]interface{}
}
