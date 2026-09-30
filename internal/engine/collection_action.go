package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// collectionActionExecutor runs a task whose FQCN names a registered
// Collection method, delegating anything it does not recognize to a
// fallback executor. It implements CheckExecutor too: in check mode a
// method is run through its own declared Check function, and a method that
// declared none is reported as unchecked.
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

	// pool and persist, set by WithConnectionPool, keep a device's SSH
	// connections open between its tasks when persist admits the device.
	pool    *remoteexec.Pool
	persist PersistFunc

	// seed, set by WithLoginSeeder, resolves the login a method whose
	// manifest sets SeedsLogin gives to the machine it creates.
	seed LoginSeeder
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
//
// mode says which of desc's two functions the invoker must run
// (collection.Descriptor.MethodFor). It is passed explicitly rather than
// left for the invoker to assume, because an invoker that ignored it would
// run Invoke, and change the device, for a caller that asked only for a
// check. By the time an invoker is called in check mode, desc has already
// been confirmed to support it.
type CollectionInvoker func(ctx context.Context, desc collection.Descriptor, device inventory.InventoryItem, params map[string]interface{}, mode collection.Mode) (collection.Result, map[string]interface{}, error)

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
	return e.run(ctx, task, device, desc, collection.ModeExecute)
}

// Check implements CheckExecutor: it runs task's registered Collection
// method through its declared Check function, and hands anything the
// registry does not know to the fallback's own check.
func (e *collectionActionExecutor) Check(ctx context.Context, task *Task, device inventory.InventoryItem) (ActionResult, error) {
	desc, ok := collection.Lookup(task.FQCN)
	if !ok {
		return checkThrough(ctx, e.fallback, task, device)
	}
	return e.run(ctx, task, device, desc, collection.ModeCheck)
}

// run is the dispatch Execute and Check share: the same status, capability
// and credential handling for both modes, differing only in which of the
// method's two functions is finally called. Sharing it is what keeps a
// check honest about the refusals a real run would hit: a check of a
// declared stub, or of a method against a device missing its capability,
// fails exactly the way the real run would.
func (e *collectionActionExecutor) run(ctx context.Context, task *Task, device inventory.InventoryItem, desc collection.Descriptor, mode collection.Mode) (ActionResult, error) {

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
	// The same transport check pleiades validate makes (TransportRule),
	// again here for the reason checkMethodCapabilities gives: a plan can
	// run against an inventory that changed after it was checked. It is
	// what keeps exec.command, an SSH method, off a Windows server that
	// has CommandExecCapable through WindowsShellCapable.
	if err := collection.CheckTransports(device, task.FQCN, desc.Manifest); err != nil {
		return ActionResult{}, err
	}

	// The method's own declared answer, resolved once, before any
	// credential is read or subprocess spawned. A method with no check
	// support is not a failure in check mode: it is reported by name as
	// unchecked, and the rest of the runbook is still checked.
	method, err := desc.MethodFor(mode)
	if err != nil {
		if mode == collection.ModeCheck {
			return ActionResult{}, &UncheckedError{FQCN: task.FQCN, Reason: desc.NoCheckAnswer()}
		}
		return ActionResult{}, err
	}

	var seed map[string]string
	if param := desc.Manifest.SeedsLogin; param != "" {
		if seed, err = e.seedLogin(ctx, task.FQCN, param, desc.Manifest.SeedsLoginPassword, task.Params); err != nil {
			return ActionResult{}, err
		}
	}

	if e.invoke != nil {
		result, stats, err := e.invoke(ctx, desc, device, task.Params, mode)
		if err != nil {
			return ActionResult{}, methodError(task.FQCN, mode, err)
		}
		if err := holdReadOnly(desc, task.FQCN, result.Changed, stats); err != nil {
			return ActionResult{}, err
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
	if seed != nil {
		seeded, ok := rc.(*runbookContext)
		if !ok {
			return ActionResult{}, fmt.Errorf("collection method %q seeds a login, which this run's context cannot carry", task.FQCN)
		}
		seeded.addSecrets(seed)
	}

	// The credential the method is handed joins the run's masking set, even
	// when it fails, so an error or a stat echoing it back is masked at the
	// output boundary like a register_mask'd value is.
	secrets := credentialSecrets(rc.InjectSecrets())

	e.lendPool(ctx, rc, device)
	result, err := method(ctx, rc, device, task.Params)
	e.endLoginSession(desc, device, mode)
	if err != nil {
		return ActionResult{Secrets: secrets}, methodError(task.FQCN, mode, err)
	}

	stats := map[string]interface{}{}
	if collector, isCollector := rc.(FactCollector); isCollector {
		stats = collector.Facts()
	}
	if err := holdReadOnly(desc, task.FQCN, result.Changed, stats); err != nil {
		return ActionResult{Secrets: secrets}, err
	}

	return ActionResult{Changed: result.Changed, Stats: stats, Secrets: secrets}, nil
}

// holdReadOnly fails a built-in method that declares it only reads
// (Reversibility.ReadOnly) and reported a change or an undo anyway.
//
// The declaration is what a rollback relies on to say a failed read-only
// task left nothing behind, and what lets a runbook marked reversible run
// read-only tasks with no rollback step of their own. A claim those two
// lean on is held at run time rather than trusted. A diff is not held
// against it: a read-only method may report one saying nothing changed
// (sdk.Unchanged), as wait.connection does. An external Collection
// program's declaration is not honored at all (Descriptor.Provider), so it
// is not held either.
func holdReadOnly(desc collection.Descriptor, fqcn string, changed bool, stats map[string]interface{}) error {
	if !desc.Manifest.Reversibility.ReadOnly || desc.Provider != nil {
		return nil
	}
	if changed {
		return fmt.Errorf("collection method %q declares that it only reads, and reported a change", fqcn)
	}
	if _, undo := stats[sdk.StatInverse]; undo {
		return fmt.Errorf("collection method %q declares that it only reads, and recorded an undo", fqcn)
	}
	return nil
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

// methodError is what a method's error means for its task. In a check, a
// method's answer that it cannot check this call
// (collection.CannotCheckError, however far it was wrapped or however many
// processes it crossed) reports the task unchecked, naming the method's
// reason, like a method with no check support at all. Anything else, and
// that same answer outside a check, is the task's failure.
func methodError(fqcn string, mode collection.Mode, err error) error {
	var cannot *collection.CannotCheckError
	if mode == collection.ModeCheck && errors.As(err, &cannot) {
		return &UncheckedError{FQCN: fqcn, Reason: cannot.Reason}
	}
	return fmt.Errorf("collection method %q: %w", fqcn, err)
}

// credentialSecrets returns the values of a method's credential that output
// must never show: every value except the ones a credential names as
// identifiers (its username, a certificate's public half, a seeded login's
// username and public key). The SSH transport's own masking leaves the same
// identifiers readable (action_ssh.go), and for the same reason: masking is
// a substring scrub, so a short username such as root would scrub every
// path under /root out of a run's report. Everything else is masked,
// including a key this list has never heard of, so a secret field added to
// a credential later is masked by default.
func credentialSecrets(secrets map[string]string) []string {
	out := make([]string, 0, len(secrets))
	for key, value := range secrets {
		switch key {
		case wire.SecretUsername, wire.SecretCertificatePEM, wire.SecretSeedUsername, wire.SecretSeedAuthorizedKey:
			continue
		}
		out = append(out, value)
	}
	return out
}
