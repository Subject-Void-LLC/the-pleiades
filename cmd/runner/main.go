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
	"errors"
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
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
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
	// Argument routing is the literal first statement in main(), before
	// any flag parsing, NATS connection, or telemetry setup. Both
	// non-default routes need that. A spawned collection-runner child
	// (internal/adapters/native's own per-task subprocess boundary,
	// PLAN.md Section 17.5) must pay for none of it and must never itself
	// try to become a second Runner Agent; and the container probe
	// (healthcheck.go) runs every few seconds for the life of the
	// container, so it must stay fast and hold nothing.
	//
	// routeFor rather than two inline conditions, so the ORDER of the
	// guards is a value TestRouteFor can assert on. See its own doc
	// comment for why that distinction is not academic.
	switch routeFor(os.Args[1:]) {
	case routeCollectionChild:
		os.Exit(native.RunCollectionChild(context.Background()))
	case routeHealthcheck:
		os.Exit(runHealthcheck(os.Args[1:]))
	case routeAgent:
		// Fall through into the body below, which is the Agent.
	}

	natsURL := getenv("NATS_URL", nats.DefaultURL)

	// The one number that says how long this deployment promises to
	// survive a link outage. Every retention-shaped window in
	// internal/topology derives from it, so the two binaries must be
	// given the same value or they will disagree about the stream's
	// shape and say so on every start.
	outageBudget, err := topology.ParseOutageBudget(os.Getenv("PLEIADES_MAX_OUTAGE"))
	if err != nil {
		log.Fatalf("invalid PLEIADES_MAX_OUTAGE: %v", err)
	}

	// This process handles secrets more directly than any other: it holds
	// a dispatch's credentials in memory and shells out to real transports
	// with them, so it is the last place that should log unmasked.
	//
	// Before Phase 22 this line read slog.Default(), the unconfigured
	// process default, which meant plain text to stderr with no masking of
	// any kind. Both halves matter and both are here: the structured path
	// through ReplaceAttr, and the standard library's log package, which
	// this file alone calls a dozen times on its fatal paths and which
	// bypasses slog entirely.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, redact.Shared().HandlerOptions(slog.LevelInfo)))
	slog.SetDefault(logger)
	log.SetOutput(redact.Shared().Writer(os.Stderr))

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
	bus, err := event.NewNatsBus(ctx, natsURL, logger, topology.StreamReader, outageBudget, false)
	if err != nil {
		log.Fatalf("failed to connect event bus: %v", err)
	}

	// A second, independent connection backs the raw jetstream.Consumer
	// Agent pulls from (PATTERNS.md's "Push vs Pull Execution Model" entry:
	// this is deliberate, not routed through Bus.Subscribe) and the
	// jetstream.JetStream handle Agent's own Dead Letter Queue handling
	// needs. Same documented two-connection tradeoff cmd/controller and
	// cmd/demo already accept. It carries topology.DialOptions like every
	// other dial in the module, which for this connection specifically is
	// what keeps a Runner pulling work after a link outage longer than two
	// minutes instead of going quiet forever.
	nc, err := topology.Connect(ctx, natsURL, logger, "runner-dispatch")
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
	lockMgr, err := lock.NewNatsLockManager(ctx, natsURL, logger, topology.StreamReader)
	if err != nil {
		log.Fatalf("failed to init lock manager: %v", err)
	}

	poolSize := envInt("RUNNER_POOL_SIZE", 0) // 0 means "unset"; NewAgent's own WithPoolSize ignores n<=0 and keeps defaultPoolSize
	agentOpts := []runner.AgentOption{runner.WithPoolSize(poolSize)}

	// Consumer-side duplicate suppression. A dispatch publish can report
	// failure to the Controller and have succeeded anyway, and the
	// Controller's stale-job reclaim then republishes it well outside
	// JetStream's producer-side duplicate window, so the same unit of
	// work can arrive twice. This is what recognises the second one.
	//
	// A Runner that cannot reach the bucket starts without the check
	// rather than refusing to start: the check is an improvement on a
	// narrow hazard, and trading it for unavailability would be the wrong
	// way round.
	if dedupKV, err := topology.BindDedupBucket(ctx, js, topology.StreamReader); err != nil {
		logger.Warn("dispatch duplicate suppression is disabled: could not bind the dedup bucket",
			"error", err.Error())
	} else {
		agentOpts = append(agentOpts, runner.WithDedupStore(
			event.NewNatsDedupStore(dedupKV), topology.DerivedDedupTTLFloor(outageBudget)))
	}

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

	// The liveness heartbeat (internal/runner/heartbeat.go), which is
	// what `runner healthcheck` reads and therefore what an orchestrator
	// probe really asks. ON by default, unlike the WAL above, because a
	// probe an operator has to opt into is a probe most deployments will
	// not have, and the failure it covers (FAILURE_PATTERNS.md #119: this
	// process stays alive and stops doing any work when its NATS
	// connection closes for good) is silent by construction.
	//
	// It is handed `consumer`, the exact durable consumer the Agent pulls
	// from, and nothing else. That is the whole design: a beat is written
	// only after a real round trip to the server about that consumer, so
	// severing the connection stops the beats. See heartbeat.go's package
	// comment for what that proves and what it misses.
	//
	// Fail-closed at startup, the same shape every other Runner
	// dependency above already is: an unwritable heartbeat path is an
	// operator error to fix now, not a surprise the first tick discovers
	// after this process has started claiming work.
	if hbPath := heartbeatPath(); hbPath != "" {
		interval, err := heartbeatInterval()
		if err != nil {
			log.Fatalf("failed to read the heartbeat interval: %v", err)
		}
		heartbeat, err := runner.NewHeartbeat(hbPath, interval, consumer, logger)
		if err != nil {
			log.Fatalf("failed to init the liveness heartbeat: %v", err)
		}
		agentOpts = append(agentOpts, runner.WithHeartbeat(heartbeat))
		logger.Info("liveness heartbeat enabled",
			slog.String("path", hbPath.String()), slog.Duration("interval", interval))
	} else {
		// Warn, not Info: this is a deployment choosing to have no
		// liveness signal at all, and the line has to be findable when
		// somebody later asks why the probe reports a caller error.
		logger.Warn("liveness heartbeat disabled",
			slog.String("reason", heartbeatFileEnv+" is set and empty, so nothing is written and `runner healthcheck` has nothing to read"))
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
	// errors.Is rather than !=: agent_run.go returns ctx.Err() unwrapped
	// today, so a bare comparison happens to work, but any wrapping added
	// to the fetch loop turns a clean SIGTERM shutdown into a Fatalf exit
	// 1, which under a restartPolicy of Always reads as a crash loop on
	// every rolling update.
	if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
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
