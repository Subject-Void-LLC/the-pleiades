// Package native implements runner.ExecutionAdapter for native Go
// collections, by handing a wire.DispatchPayload back to the same
// internal/engine execution stack (Executor, TransportActionExecutor,
// CollectionActionExecutor) the Crawl-tier CLI (cmd/pleiades/run.go)
// already runs, rather than a second, parallel dispatch mechanism.
package native

import (
	"context"
	"fmt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"log/slog"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	serialtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serial"
	serialtcptransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serialtcp"
	sshtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/ssh"
	telnettransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/telnet"
	winrmtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/winrm"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialtcp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Adapter implements runner.ExecutionAdapter for the native Go execution
// path. bindings is built once, at construction, from the real
// transports: internal/transport/transport.go's own doc comment states
// this explicitly ("Phase 16 places the same transport behind the runner
// mesh; it does not own the transport itself"), so this package
// constructs no transport of its own beyond wiring sshtransport.New(...),
// serialtransport.New(...), serialtcptransport.New(...), and
// telnettransport.New(...) (Phase 73) into the one shared,
// Registry-backed constructor (engine.NewDefaultTransportBindings)
// cmd/pleiades/run.go also builds from.
type Adapter struct {
	bus      event.Bus
	runbooks runbook.Source
	bindings map[string]engine.TransportBinding
	ipc      *ipcCollectionExecutor
	logger   *slog.Logger

	// renderer renders a dispatched task's params that hold a template
	// (WithRenderer); nil refuses such a task rather than handing the
	// method the literal text.
	renderer render.Engine
}

// AdapterOption configures an Adapter at construction.
type AdapterOption func(*Adapter)

// WithRenderer hands the Adapter the template renderer its runs render
// task params through (engine.WithRenderer). cmd/runner, the composition
// root, passes the one engine it holds.
func WithRenderer(eng render.Engine) AdapterOption {
	return func(a *Adapter) { a.renderer = eng }
}

// NewAdapter builds a native Adapter. runbooks resolves a dispatched
// RunbookID to its compiled *engine.DAG (internal/runbook.Source, the
// same port cmd/controller's own dispatch.Worker already depends on).
// logger is injected, never the package-level slog.* global
// (internal/telemetry/telemetry.go's own stated rule for observability
// values); a nil logger falls back to slog.Default() at construction
// only, matching internal/runner.Agent's own established convention.
//
// It fails closed if this process's own executable path cannot be
// resolved (newIPCCollectionExecutor): every Collection method this
// Adapter can reach runs behind the per-task subprocess boundary PLAN.md
// Section 17.5 requires, which re-execs this same binary, so a Runner
// that cannot find its own path has no business starting up.
func NewAdapter(bus event.Bus, runbooks runbook.Source, logger *slog.Logger, opts ...AdapterOption) (*Adapter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	ipc, err := newIPCCollectionExecutor(logger)
	if err != nil {
		return nil, fmt.Errorf("failed to init collection subprocess executor: %w", err)
	}
	a := &Adapter{
		bus:      bus,
		runbooks: runbooks,
		bindings: engine.NewDefaultTransportBindings(
			sshtransport.New(sshtransport.Options{}),
			serialtransport.New(serialexec.Options{}),
			serialtcptransport.New(serialtcp.Options{}, remoteexec.Options{}),
			telnettransport.New(telnetexec.Options{}, remoteexec.Options{}),
			winrmtransport.New(winrmexec.Options{}),
		).All(),
		ipc:    ipc,
		logger: logger,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// runbookContextFor is the newContext function
// engine.NewCollectionActionExecutor calls to build the sdk.RunbookContext
// a Collection method's Invoke receives, bound to one dispatch: its
// secrets are the ones the Controller attached to payload at dispatch time
// (PLAN.md Section 17's Just-in-Time delivery principle), whatever device
// type the Runner rebuilt, so no second credential lookup happens here.
// It never fails: the secrets are already in hand. It is the Crawl tier
// that has a credential store to read and therefore a failure to report,
// which is why engine.RunbookContextFunc carries an error at all.
func runbookContextFor(payload wire.DispatchPayload) engine.RunbookContextFunc {
	return func(context.Context, inventory.InventoryItem) (sdk.RunbookContext, error) {
		return engine.NewRunbookContext(payload.Secrets), nil
	}
}

// Execute implements runner.ExecutionAdapter. It resolves payload's
// runbook to a compiled DAG, adapts payload into an inventory.InventoryItem
// (wireDevice), and runs the identical engine.Executor/ActionExecutor
// stack the Crawl-tier CLI runs, scoped to the one device this payload
// names.
func (a *Adapter) Execute(ctx context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	// The mode is settled before anything starts, and a value that is not
	// a mode refuses the dispatch: reading it as a real run would be the
	// one unsafe guess (pkg/wire.DispatchPayload.Mode).
	mode, err := collection.ParseMode(payload.Mode)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("refusing dispatch of runbook %q: %w", payload.RunbookID, err)
	}

	started := wire.JobEvent{Status: "started", Host: payload.DeviceHost, Task: "runbook:" + payload.RunbookID}
	started.Timestamp = time.Now().UTC().Format(time.RFC3339)
	started.EventData.Message = fmt.Sprintf("started runbook %q on %s", payload.RunbookID, payload.DeviceName)
	if mode == collection.ModeCheck {
		started.EventData.Message = fmt.Sprintf("started a check of runbook %q on %s: nothing will be changed", payload.RunbookID, payload.DeviceName)
	}
	if err := a.publish(ctx, payload.JobID, started); err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to publish started event: %w", err)
	}

	// The run-time backstop, before anything is resolved or executed:
	// injected material this path cannot honour fails the dispatch loudly
	// rather than running it with part of its credentials missing. See
	// inject.go for why this is a refusal and not a silent skip, and for
	// why file is refused for a stronger reason than env.
	if err := refuseUnsupportedInjection(payload.Injected); err != nil {
		return wire.Outcome{}, err
	}

	variables, withheld, err := injectedVariables(payload.ExtraVars, payload.Injected)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to merge injected extra variables for %s: %w", payload.DeviceName, err)
	}
	// A variable withheld because it holds a secret (inject.go) is named in
	// the job's log, never its value, so an author whose condition reads it
	// learns why it is not there rather than guessing.
	for _, name := range withheld {
		warning := wire.JobEvent{Status: "ok", Host: payload.DeviceHost, Task: "task.warning"}
		warning.Timestamp = time.Now().UTC().Format(time.RFC3339)
		warning.EventData.Message = fmt.Sprintf("WARNING: the bound credential's extra variable %q holds a secret, so runbook expressions cannot read it; a native method receives its credential directly", name)
		if err := a.publish(ctx, payload.JobID, warning); err != nil {
			return wire.Outcome{}, fmt.Errorf("failed to publish a warning: %w", err)
		}
	}

	dag, err := a.runbooks.GetDAG(ctx, payload.RunbookID)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to resolve runbook %q: %w", payload.RunbookID, err)
	}

	device, err := dispatchedDevice(payload)
	if err != nil {
		return wire.Outcome{}, a.refuse(ctx, payload, err)
	}

	// A rollback runs its own steps in place of the runbook's tasks, each
	// held to this Runner's copy of the runbook first (rollback.go).
	var rollbackOption engine.ExecutorOption = func(*engine.Executor) {}
	if payload.Rollback != nil {
		dag, rollbackOption, err = rollbackDAG(dag, payload)
		if err != nil {
			return wire.Outcome{}, a.refuse(ctx, payload, err)
		}
	}

	// The same plan-time checks `pleiades validate` and `pleiades run`
	// make, before any task runs, against the one device this dispatch
	// names and resolved exactly as the executor below resolves it. The
	// Runner used to go straight from the cached DAG to the executor, so
	// an unregistered or declared-only method, an undeclared parameter,
	// or check_mode on an uncheckable task failed only when its own task
	// was reached, after earlier tasks had already changed the device.
	if err := validateDispatch(dag, device, mode); err != nil {
		// Ended in the job's own log, like any other finished run, so a
		// reader sees why nothing ran rather than a run that started and
		// never finished. The findings name tasks, methods, parameter
		// names and this device, never a value.
		return wire.Outcome{}, a.refuse(ctx, payload, err)
	}
	credentials := credential.NewStaticStore(payload.Secrets)

	// A dispatch whose connections persist runs its Collection calls
	// through one child for its whole life, whose pool keeps the device's
	// SSH login open between tasks (ipc_session.go). Otherwise each call
	// spawns a child of its own and logs in afresh.
	invoke := a.ipc.forDispatch(payload).invoke
	if payload.PersistConnections {
		session := a.ipc.newSession(payload)
		defer session.Close()
		invoke = session.invoke
	}
	actions := engine.NewCollectionActionExecutor(
		// nil inventory.Repository: this per-task subprocess has no live
		// database connection of its own (see
		// engine.NewTransportActionExecutor's own doc comment for the
		// full reasoning), so hop-chain resolution is skipped entirely
		// here, exactly a direct connection.
		engine.NewTransportActionExecutor(a.bindings, credentials, nil, engine.NewBuiltinActionExecutor()),
		runbookContextFor(payload),
		engine.WithCollectionInvoker(invoke),
	)

	// A fresh, private lock.Manager, never a real distributed one: this
	// Executor's own per-device lease (executor.go's runOne) would
	// otherwise double-acquire the identical key
	// internal/runner.Agent.executeWithLease already holds a real,
	// distributed lease on, one call frame up, for the whole duration of
	// this Execute call. That outer lease is the only cross-Runner
	// exclusivity that matters here; this inner one only has to keep two
	// concurrent nodes within the same DAG level from racing each other
	// against the one device this call is scoped to.
	locks := lock.NewInProcessManager()
	defer locks.Close()

	// A fresh, private event.Bus, never a.bus: Executor's own internal
	// per-node lifecycle publish (executor.go) targets a subject keyed by
	// dag.ID/nodeID, unrelated to payload.JobID's own job-log stream this
	// package publishes to. Handing it a's real bus would risk that
	// internal, unrelated event stream colliding with or duplicating this
	// Adapter's own job.log events; this is a deliberate non-consumption
	// of that Executor feature, not an oversight.
	nodeBus := event.NewInProcessBus()
	defer nodeBus.Close()

	// forks and limit (payload.Fields) are deliberately not consumed here.
	// engine.NewExecutor's maxConcurrency parameter is the only knob that
	// could stand in for "forks," but singleDeviceResolver always resolves
	// every task to this call's one already-selected device (resolver.go),
	// and every device-targeting task acquires an exclusive per-device
	// lock before running (executor.go's runOne): two tasks racing the
	// same device serialize on that lock regardless of maxConcurrency, so
	// there is no concurrency dimension within one Execute call for forks
	// to bound. forks is how many devices of one job run at once, which is
	// decided where devices are dispatched: internal/dispatch's forks
	// window (window.go) holds each device back until the job has room.
	// limit is NOT read anywhere yet, although device selection happens in
	// internal/dispatch's fan-out, where it belongs; that is recorded as an
	// open item (FAILURE_PATTERNS.md #116's shape) rather than claimed.
	var journalSink engine.Journal = newJournalPublisher(ctx, a.bus, a.logger, payload.JobID, payload.DeviceID)

	executor := engine.NewExecutor(
		singleDeviceResolver{device: device},
		actions,
		locks,
		nodeBus,
		engine.NewInProcessWorkflowContext(),
		0,
		// The launch's own extra variables plus whatever a bound credential
		// injected, already merged with a collision refused. This is the
		// one injector target the native path DOES honour, and it is
		// pre-existing machinery rather than something built for it:
		// engine.WithVariables is what the Crawl-tier CLI already uses.
		engine.WithVariables(variables),
		// Task params that hold a template render through the one engine
		// the composition root handed this Adapter (render_params.go).
		engine.WithRenderer(a.renderer),
		engine.WithTaskTimeout(taskTimeout(launch.Fields(payload.Fields))),
		// The run journal (Phase 40), published onto this job's own
		// journal subject for the Controller to store. Built here rather
		// than on the Adapter so it is per dispatch, which is what makes
		// it safe for the Agent's concurrent workers without a lock of
		// its own, and which is also where JobID and the delivery's
		// attempt are both in hand.
		//
		// Declared as the interface, never as the concrete type:
		// engine.WithJournal guards a nil interface and deliberately not
		// a typed nil, so a *journalPublisher variable holding nil would
		// pass the guard and panic at the first level barrier.
		engine.WithJournal(journalSink),
		// A check writes no journal, whatever sink it is given
		// (engine.WithMode), and admits a simulate-locked device.
		engine.WithMode(mode),
		// An external program's check runs only for a job whose launcher
		// could run it for real, which the Controller says on the payload.
		engine.WithExternalChecks(payload.ExternalChecks),
		// A rollback journals as one: each entry names the job it undoes
		// and the node it undoes, which is what a later rollback of that
		// job resumes from.
		rollbackOption,
	)

	result, runErr := executor.Run(ctx, dag)
	if runErr != nil {
		return wire.Outcome{}, fmt.Errorf("execution aborted: %w", runErr)
	}

	changed := false
	for _, node := range result.Nodes {
		if node.Changed {
			changed = true
		}
	}

	// Every secret worth masking: what this run's own register_mask/
	// secret_mask annotations discovered, plus every value the Controller
	// attached to this payload, since a task's output can echo either one
	// back (internal/engine/action_ssh.go's transportActionExecutor
	// already applies this identical pairing for the Crawl-tier CLI).
	// Plus every value a bound credential injected, which a task's output
	// can echo back exactly as readily as one the Controller attached to
	// Secrets.
	injectedValues := injectedSecretValues(payload.Injected)
	secrets := make([]string, 0, len(result.Secrets)+len(payload.Secrets)+len(injectedValues))
	secrets = append(secrets, result.Secrets...)
	for _, v := range payload.Secrets {
		secrets = append(secrets, v)
	}
	secrets = append(secrets, injectedValues...)

	// Each warning a task recorded (sdk.StatWarnings) reaches the job log
	// once per dispatch, masked, before the completion that summarizes it.
	for _, warning := range runWarnings(result, secrets) {
		event := wire.JobEvent{Status: "ok", Host: payload.DeviceHost, Task: "task.warning"}
		event.Timestamp = time.Now().UTC().Format(time.RFC3339)
		event.EventData.Message = "WARNING: " + warning
		if err := a.publish(ctx, payload.JobID, event); err != nil {
			return wire.Outcome{}, fmt.Errorf("failed to publish a warning: %w", err)
		}
	}

	status, message := summarize(result, changed, secrets)
	if mode == collection.ModeCheck {
		message = "check: " + message + checkSummary(result)
	}
	completed := wire.JobEvent{Status: status, Host: payload.DeviceHost, Task: "task.completed"}
	completed.Timestamp = time.Now().UTC().Format(time.RFC3339)
	completed.EventData.Message = message
	if err := a.publish(ctx, payload.JobID, completed); err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to publish completion event: %w", err)
	}

	if result.HasErrors() {
		return wire.Outcome{}, fmt.Errorf("execution failed: %s", message)
	}
	return wire.Outcome{Unchecked: uncheckedCount(result)}, nil
}

// refuse ends a dispatch that runs nothing: it reports why in the job's
// own log, like any other finished run, and returns the error the Agent
// reports as the device's result.
func (a *Adapter) refuse(ctx context.Context, payload wire.DispatchPayload, why error) error {
	refused := wire.JobEvent{Status: "failed", Host: payload.DeviceHost, Task: "task.completed"}
	refused.Timestamp = time.Now().UTC().Format(time.RFC3339)
	refused.EventData.Message = why.Error()
	if pubErr := a.publish(ctx, payload.JobID, refused); pubErr != nil {
		return fmt.Errorf("failed to publish the refusal of runbook %q: %w", payload.RunbookID, pubErr)
	}
	return fmt.Errorf("refusing dispatch of runbook %q: %w", payload.RunbookID, why)
}

// summarize derives the one status word and message Execute's final
// job.log event reports from a completed RunResult: "failed" (with every
// node error, masked, joined) if any node errored, otherwise "changed" or
// "ok" per the Convergence principle (report changed only when something
// actually changed).
func summarize(result engine.RunResult, changed bool, secrets []string) (status, message string) {
	if result.HasErrors() {
		var errs []string
		for _, node := range result.Nodes {
			if node.Err != nil {
				errs = append(errs, redact.Text(secrets, node.Err.Error()))
			}
		}
		return "failed", strings.Join(errs, "; ")
	}
	if changed {
		return "changed", "native execution finished successfully"
	}
	return "ok", "native execution finished successfully"
}

// publish wraps evt and publishes it to topology.LogSubject(jobID),
// returning any error to the caller rather than logging and swallowing
// it: a job-log publish failure is a real signal the Runner's own
// WAL/retry machinery (internal/runner/agent_wal.go) should see, not an
// observability side effect this Adapter is entitled to hide.
func (a *Adapter) publish(ctx context.Context, jobID string, evt wire.JobEvent) error {
	return publishJobEvent(ctx, a.bus, jobID, evt)
}

// checkSummary is what a check adds to its completion message: how many
// tasks it could not check, since a check that passed over some of them
// has not looked at the whole plan, and that nothing was changed.
func checkSummary(result engine.RunResult) string {
	if unchecked := uncheckedCount(result); unchecked > 0 {
		return fmt.Sprintf("; %d task(s) could not be checked, so this check does not cover them; nothing was changed", unchecked)
	}
	return "; nothing was changed"
}

// uncheckedCount is how many of result's tasks a check could not check,
// the number Execute reports in its wire.Outcome and checkSummary in its
// message, counted once so the two cannot disagree.
func uncheckedCount(result engine.RunResult) int {
	n := 0
	for _, node := range result.Nodes {
		if node.Unchecked {
			n++
		}
	}
	return n
}

// runWarnings collects the warnings every task of result recorded, masked
// and each once, in the order they first appear.
func runWarnings(result engine.RunResult, secrets []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, node := range result.Nodes {
		var warnings []string
		switch list := node.Stats[sdk.StatWarnings].(type) {
		case []string:
			warnings = list
		case []any:
			for _, w := range list {
				warnings = append(warnings, fmt.Sprint(w))
			}
		}
		for _, w := range warnings {
			masked := redact.Text(secrets, w)
			if !seen[masked] {
				seen[masked] = true
				out = append(out, masked)
			}
		}
	}
	return out
}
