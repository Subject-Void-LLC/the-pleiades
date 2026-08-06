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
// Deliberately out of scope: choosing between native.Adapter and
// ansible.ReceptorAdapter at runtime (PATTERNS.md's Strangler Fig entry).
// ansible.ReceptorAdapter does not implement runner.ExecutionAdapter today
// (it has no Execute(ctx, DispatchPayload) error method, only
// StreamMockJob, a UI-scaffolding helper cmd/demo uses directly); a real
// adapter-selection mechanism is future work, not invented here to fill a
// binary that only has one real choice to make regardless.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/SubjectVoidLLC/the-pleiades/internal/adapters/native"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/SubjectVoidLLC/the-pleiades/internal/telemetry"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
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

func main() {
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

	adapter := native.NewAdapter(bus)
	agent := runner.NewAgent(consumer, adapter, js, topology.MaxDeliverDefault, logger,
		tracerProvider.Tracer("github.com/SubjectVoidLLC/the-pleiades/internal/runner"))

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
