// Package native implements runner.ExecutionAdapter for native Go
// collections, by handing a wire.DispatchPayload back to the same
// internal/engine execution stack (Executor, TransportActionExecutor,
// CollectionActionExecutor) the Walk-tier CLI (cmd/pleiades/run.go)
// already runs, rather than a second, parallel dispatch mechanism.
package native

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	sshtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/ssh"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Adapter implements runner.ExecutionAdapter for the native Go execution
// path. bindings is built once, at construction, from a real SSH
// transport: internal/transport/transport.go's own doc comment states
// this explicitly ("Phase 16 places the same transport behind the runner
// mesh; it does not own the transport itself"), so this package
// constructs no transport of its own beyond wiring sshtransport.New(...)
// into the one shared, Registry-backed constructor
// (engine.NewDefaultTransportBindings) cmd/pleiades/run.go also builds
// from.
type Adapter struct {
	bus      event.Bus
	runbooks runbook.Source
	bindings map[string]engine.TransportBinding
	ipc      *ipcCollectionExecutor
	logger   *slog.Logger
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
func NewAdapter(bus event.Bus, runbooks runbook.Source, logger *slog.Logger) (*Adapter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	ipc, err := newIPCCollectionExecutor(logger)
	if err != nil {
		return nil, fmt.Errorf("failed to init collection subprocess executor: %w", err)
	}
	return &Adapter{
		bus:      bus,
		runbooks: runbooks,
		bindings: engine.NewDefaultTransportBindings(sshtransport.New(sshtransport.Options{})).All(),
		ipc:      ipc,
		logger:   logger,
	}, nil
}

// newDeviceRunbookContext is the newContext function
// engine.NewCollectionActionExecutor calls to build the sdk.RunbookContext
// a Collection method's Invoke receives. device is always the *wireDevice
// this package's own Execute built for this call (singleDeviceResolver
// never resolves any other value), so its embedded wire.DispatchPayload
// already carries whatever secrets the Controller attached at dispatch
// time (PLAN.md Section 17's Just-in-Time delivery principle) -- no
// second credential lookup happens here. The type-assertion fallback is a
// defensive measure against a structural invariant of this package's own
// composition, not an expected runtime case: it can only be reached if a
// future change hands engine.Executor a TargetResolver other than
// singleDeviceResolver.
func newDeviceRunbookContext(device inventory.InventoryItem) sdk.RunbookContext {
	wd, ok := device.(*wireDevice)
	if !ok {
		return engine.NewRunbookContext(nil)
	}
	return engine.NewRunbookContext(wd.payload.Secrets)
}

// Execute implements runner.ExecutionAdapter. It resolves payload's
// runbook to a compiled DAG, adapts payload into an inventory.InventoryItem
// (wireDevice), and runs the identical engine.Executor/ActionExecutor
// stack the Walk-tier CLI runs, scoped to the one device this payload
// names.
func (a *Adapter) Execute(ctx context.Context, payload wire.DispatchPayload) error {
	started := wire.JobEvent{Status: "started", Host: payload.DeviceHost, Task: "runbook:" + payload.RunbookID}
	started.Timestamp = time.Now().UTC().Format(time.RFC3339)
	started.EventData.Message = fmt.Sprintf("started runbook %q on %s", payload.RunbookID, payload.DeviceName)
	if err := a.publish(ctx, payload.JobID, started); err != nil {
		return fmt.Errorf("failed to publish started event: %w", err)
	}

	dag, err := a.runbooks.GetDAG(ctx, payload.RunbookID)
	if err != nil {
		return fmt.Errorf("failed to resolve runbook %q: %w", payload.RunbookID, err)
	}

	device := newWireDevice(payload)
	credentials := credential.NewStaticStore(payload.Secrets)
	actions := engine.NewCollectionActionExecutor(
		engine.NewTransportActionExecutor(a.bindings, credentials, engine.NewBuiltinActionExecutor()),
		newDeviceRunbookContext,
		engine.WithCollectionInvoker(a.ipc.invoke),
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
	// to bound. Wiring it through anyway would set a real parameter to a
	// real value with no observable effect, exactly the "correctly
	// computed and never actually read" shape FAILURE_PATTERNS.md #116
	// already named for this same job's Fields before this phase.
	// limit has the identical non-answer: device selection already
	// happened upstream, in internal/dispatch's own fan-out, before this
	// payload ever existed. See LESSONS_LEARNED.md for the recorded rule.
	executor := engine.NewExecutor(
		singleDeviceResolver{device: device},
		actions,
		locks,
		nodeBus,
		engine.NewInProcessWorkflowContext(),
		0,
		engine.WithVariables(payload.ExtraVars),
		engine.WithTaskTimeout(taskTimeout(launch.Fields(payload.Fields))),
	)

	result, runErr := executor.Run(ctx, dag)
	if runErr != nil {
		return fmt.Errorf("execution aborted: %w", runErr)
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
	// already applies this identical pairing for the Walk-tier CLI).
	secrets := make([]string, 0, len(result.Secrets)+len(payload.Secrets))
	secrets = append(secrets, result.Secrets...)
	for _, v := range payload.Secrets {
		secrets = append(secrets, v)
	}

	status, message := summarize(result, changed, secrets)
	completed := wire.JobEvent{Status: status, Host: payload.DeviceHost, Task: "task.completed"}
	completed.Timestamp = time.Now().UTC().Format(time.RFC3339)
	completed.EventData.Message = message
	if err := a.publish(ctx, payload.JobID, completed); err != nil {
		return fmt.Errorf("failed to publish completion event: %w", err)
	}

	if result.HasErrors() {
		return fmt.Errorf("execution failed: %s", message)
	}
	return nil
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
				errs = append(errs, credential.Mask(secrets, node.Err.Error()))
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
