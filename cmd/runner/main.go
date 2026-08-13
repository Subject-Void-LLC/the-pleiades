// Command runner is the Data Plane composition root PLAN.md Section 16
// names: a stateless worker that pulls dispatched Runbook jobs off the
// shared durable consumer group (topology.DispatchConsumerConfig,
// PLAN.md Section 26.4) and executes them via a real ExecutionAdapter.
//
// This is Section 25's own named deadline, the same one cmd/controller
// closes for the Control Plane side: "Composition root (cmd/controller,
// cmd/runner) | Every driver construction in the system | Build by Phase
// 2." runner.Agent and native.Adapter both existed before this phase with
// zero real callers (PATTERNS.md's Dead Letter Queue entry and this
// phase's own Adversarial Pattern Justification both named that gap); this
// binary is their first one.
//
// Adapter selection is real here (PATTERNS.md's Strangler Fig entry made
// wiring, at last): every dispatch reaches the Agent through
// internal/adapters/routing.Router, which resolves the kind the payload
// carries to the adapter that kind's descriptor declares. native.Adapter
// is always composed. legacy.Adapter (internal/adapters/legacy, Phase 17)
// is composed only when the operator supplies both PLAYBOOK_DIR and
// ANSIBLE_RUNNER_IMAGE, fail-open to native-only: a deployment that has
// never run Ansible simply has no playbook kind to route, and a playbook
// dispatch arriving anyway is refused per message (routing.ErrNoAdapter,
// which the Agent dead-letters rather than retrying forever) instead of
// crashing a Runner over a capability it was never given.
//
// Composing legacy is also what makes FAILURE_PATTERNS.md #109's recorded
// trade real rather than theoretical: internal/adapters/legacy's
// orchestration imports testcontainers-go, so a Runner built with it
// links a testing library and a Docker client. The exposure was accepted
// knowingly there, bounded by Phase 20/22's replacement of the
// orchestrator; this comment is the import's price tag, kept beside it.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	// Blank-imported so every generated Collection method registers itself
	// into pkg/collection before native.Adapter's own
	// engine.NewCollectionActionExecutor ever looks one up, mirroring
	// cmd/pleiades/catalog_builtins.go exactly. Nothing before Phase 16
	// (Native Go Execution Adapter) needed this: the fake adapter never
	// called into the Collection registry at all.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	// The built-in launch kinds. A blank import because their init()
	// functions are the only thing that populates internal/launch's
	// registry, and the Router resolves a dispatched kind to an adapter
	// through this registry, so a Runner that did not import it would
	// route nothing at all (FAILURE_PATTERNS.md #52).
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"

	"github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// serviceName identifies this process in every span it emits.
const serviceName = "pleiades-runner"

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envInt reads key as an integer, returning fallback if key is unset or
// does not parse as one. Used for RUNNER_POOL_SIZE, which runner.
// WithPoolSize itself already treats a non-positive value as "keep the
// default," so an unset or malformed env var and an explicit 0 both fall
// through to the same safe behavior.
func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func main() {
	// The re-exec check is the literal first statement in main(), before
	// any flag parsing, NATS connection, or telemetry setup: a spawned
	// collection-runner child (internal/adapters/native's own per-task
	// subprocess boundary, PLAN.md Section 17.5) must pay for none of
	// that, and must never itself try to become a second Runner Agent.
	if len(os.Args) > 1 && os.Args[1] == native.InternalCollectionRunnerArg {
		os.Exit(native.RunCollectionChild(context.Background()))
	}

	natsURL := getenv("NATS_URL", nats.DefaultURL)
	logger := slog.Default()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Tracing is built before anything else so the spans describing
	// startup itself are recorded. PLAN.md Section 19 puts OTEL in "all
	// components", and this is the far end of the trace the Controller
	// starts: without a provider here, a dispatch's trace ends at the bus.
	telemetryCfg, err := telemetry.ConfigFromEnv(serviceName, "")
	if err != nil {
		log.Fatalf("failed to read telemetry configuration: %v", err)
	}
	tracerProvider, err := telemetry.Setup(ctx, telemetryCfg)
	if err != nil {
		log.Fatalf("failed to init telemetry: %v", err)
	}

	// bus backs native.Adapter's own log-event publishing
	// (internal/adapters/native/adapter.go), and ensures the single
	// Pleiades stream (topology.EnsureStream) exists.
	bus, err := event.NewNatsBus(ctx, natsURL)
	if err != nil {
		log.Fatalf("failed to connect event bus: %v", err)
	}

	// A second, independent connection backs the raw jetstream.Consumer
	// Agent pulls from (PATTERNS.md's "Push vs Pull Execution Model" entry:
	// this is deliberate, not routed through Bus.Subscribe) and the
	// jetstream.JetStream handle Agent's own Dead Letter Queue handling
	// needs. Same documented two-connection tradeoff cmd/controller and
	// cmd/demo already accept.
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Fatalf("failed to connect to nats: %v", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		log.Fatalf("failed to get jetstream: %v", err)
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		log.Fatalf("failed to create dispatch consumer: %v", err)
	}

	// lockMgr backs Agent's own per-device execution lease (PLAN.md
	// Section 13: "Locks live in the backend... a device can only have
	// one exclusive execution running against it at a time"), acquired
	// around every job this Agent executes (internal/runner's
	// executeWithLease). A distinct NATS connection from event.NewNatsBus
	// and the raw jetstream one above, the same documented
	// multi-connection tradeoff cmd/controller's own lockMgr construction
	// already accepts.
	lockMgr, err := lock.NewNatsLockManager(ctx, natsURL)
	if err != nil {
		log.Fatalf("failed to init lock manager: %v", err)
	}

	poolSize := envInt("RUNNER_POOL_SIZE", 0) // 0 means "unset"; NewAgent's own WithPoolSize ignores n<=0 and keeps defaultPoolSize
	agentOpts := []runner.AgentOption{runner.WithPoolSize(poolSize)}

	// WAL result buffering (PLAN.md Section 16's State Desync
	// Mitigation) is opt-in: only constructed, and only fail-closed at
	// startup, when an operator actually asks for it via RUNNER_WAL_DIR.
	// A Runner that never sets this env var behaves exactly as if
	// WithResultWAL did not exist.
	if walDir := getenv("RUNNER_WAL_DIR", ""); walDir != "" {
		wal, err := runner.NewFileWAL(walDir)
		if err != nil {
			log.Fatalf("failed to init result wal: %v", err)
		}
		agentOpts = append(agentOpts, runner.WithResultWAL(wal, bus))
	}

	// runbooks resolves a dispatched RunbookID to its compiled *engine.DAG,
	// the same internal/runbook.DirSource port and RUNBOOK_DIR convention
	// cmd/controller already uses, reused rather than duplicated: this is
	// what actually gives native.Adapter something real to execute
	// (Phase 16, Native Go Execution Adapter), replacing the three
	// time.Sleep calls it used to run instead. Fail-closed at startup, the
	// same shape every other Runner dependency above already is.
	runbookDir := getenv("RUNBOOK_DIR", inventory.DefaultRunbookDir)
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		log.Fatalf("failed to init runbook source: %v", err)
	}

	nativeAdapter, err := native.NewAdapter(bus, runbooks, logger)
	if err != nil {
		log.Fatalf("failed to init native adapter: %v", err)
	}
	adapters := map[string]routing.Executor{"native": nativeAdapter}

	// The legacy Ansible adapter, composed only when the operator supplies
	// both halves of its configuration: the directory the playbooks live in
	// and the container image a real ansible-playbook runs inside. Half a
	// configuration is refused rather than half-honoured, because "I set
	// PLAYBOOK_DIR and my playbooks silently never ran" is the exact shape
	// of failure this repository keeps recording; a deployment that sets
	// neither is simply a native-only Runner, and the startup log below
	// says which kinds this process can actually run either way.
	playbookDir := getenv("PLAYBOOK_DIR", "")
	ansibleImage := getenv("ANSIBLE_RUNNER_IMAGE", "")
	if (playbookDir == "") != (ansibleImage == "") {
		log.Fatalf("PLAYBOOK_DIR and ANSIBLE_RUNNER_IMAGE must be set together: one names what to run, the other names what runs it")
	}
	if playbookDir != "" {
		playbooks, err := playbook.NewDirSource(playbookDir)
		if err != nil {
			log.Fatalf("failed to init playbook source: %v", err)
		}
		adapters["legacy"] = legacy.NewAdapter(bus, playbooks, legacy.NewDockerOrchestrator(), ansibleImage, logger)
	}

	// The Router satisfies the Agent's own one-method adapter interface,
	// so the Agent does not know it is holding a router: adding a kind
	// changes the registry and this map, never the Agent. It is composed
	// unconditionally, native-only included, so both configurations run
	// the same code path and a kind with no adapter is refused per
	// dispatch (routing.ErrNoAdapter, dead-lettered) rather than
	// misexecuted.
	router := routing.New(adapters)
	logger.Info("execution adapters composed",
		slog.String("kinds", strings.Join(router.Kinds(), ", ")))

	agent := runner.NewAgent(consumer, router, js, lockMgr, topology.MaxDeliverDefault, logger,
		tracerProvider.Tracer("github.com/Subject-Void-LLC/the-pleiades/internal/runner"), agentOpts...)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		logger.Info("shutting down")
		cancel()
	}()

	logger.Info("runner agent starting", slog.String("nats_url", natsURL), slog.String("durable", topology.DispatchDurableName))
	if err := agent.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("agent run failed: %v", err)
	}

	// Safe to close only after agent.Run has returned: Run's own
	// sync.WaitGroup drain (agent_run.go) guarantees every worker's
	// executeWithLease has already run its deferred lease.Release by the
	// time Run returns, so no in-flight lock use can still be relying on
	// this connection.
	if err := lockMgr.Close(); err != nil {
		logger.Error("lock manager close failed", slog.String("error", err.Error()))
	}

	if err := bus.Close(); err != nil {
		logger.Error("event bus drain failed", slog.String("error", err.Error()))
	}

	// Flushed last, and on its own timeout: spans describing the shutdown
	// path are only exported if the provider outlives everything that
	// emits them.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), telemetry.ShutdownTimeout)
	defer shutdownCancel()
	if err := tracerProvider.Shutdown(shutdownCtx); err != nil {
		logger.Error("telemetry shutdown failed", slog.String("error", err.Error()))
	}
}
