package engine

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
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
	// that captures emitted facts, which is what a real run needs and what
	// tests assert against.
	newContext func(device inventory.InventoryItem) sdk.RunbookContext
}

// NewCollectionActionExecutor returns an ActionExecutor that dispatches
// registered Collection methods and delegates everything else to fallback.
//
// Pass NewBuiltinActionExecutor() as fallback to keep the engine keywords
// working, which is what the composition root does.
func NewCollectionActionExecutor(fallback ActionExecutor, newContext func(inventory.InventoryItem) sdk.RunbookContext) ActionExecutor {
	return &collectionActionExecutor{fallback: fallback, newContext: newContext}
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

	rc := e.newContext(device)
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
