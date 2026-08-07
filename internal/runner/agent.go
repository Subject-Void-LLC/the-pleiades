package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/retry"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ExecutionAdapter executes a runbook against a target device. The
// payload it receives is wire.DispatchPayload, the shared DTO
// pkg/wire now owns: this package used to define its own local
// DispatchPayload type, hand-kept in sync with the near-identical one
// api/dispatcher.go separately defined, a duplication pkg/wire's own doc
// comment names as the exact bug shape (a stale DeviceIP field naming an
// "ip" property no device type in this codebase ever populates, and a
// DeviceName field one of the two duplicates populated from the wrong
// accessor) this move exists to make impossible to repeat.
type ExecutionAdapter interface {
	Execute(ctx context.Context, payload wire.DispatchPayload) error
}

// Agent is the executor node that pulls jobs from NATS and runs them.
type Agent struct {
	consumer   jetstream.Consumer
	adapter    ExecutionAdapter
	js         jetstream.JetStream
	maxDeliver int
	logger     *slog.Logger
	tracer     trace.Tracer
	baseSleep  time.Duration
	maxSleep   time.Duration
}

// NewAgent creates a new Runner Agent connected to a JetStream consumer.
//
// js and maxDeliver back handleMessage's Dead Letter Queue handling
// (PLAN.md Section 26.3): Agent deliberately stays on its own pull-based
// consumer.Fetch loop rather than going through event.Bus.Subscribe
// (PATTERNS.md's own "Push vs Pull Execution Model" entry names this pull
// model as deliberate), but it must not hand-roll a second DLQ mechanism
// either, per Section 25's Build-Once rule -- see handleMessage, which
// calls the same event.HandleDeliveryFailure natsBus.Subscribe itself
// uses. maxDeliver should match consumer's own configured MaxDeliver
// (topology.DispatchConsumerConfig's, in real use), or the two can
// disagree about when a message is actually exhausted.
//
// tracer is what makes this node the far end of PLAN.md Section 19's
// distributed trace: handleMessage continues the trace the Controller
// started, rather than beginning an unrelated one, by reading the W3C
// trace context the publishing Bus left in the message headers. A nil
// tracer means no spans, which is right for a test and wrong for a
// deployment.
func NewAgent(consumer jetstream.Consumer, adapter ExecutionAdapter, js jetstream.JetStream, maxDeliver int, logger *slog.Logger, tracer trace.Tracer) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("github.com/SubjectVoidLLC/the-pleiades/internal/runner")
	}
	return &Agent{
		consumer:   consumer,
		adapter:    adapter,
		js:         js,
		maxDeliver: maxDeliver,
		logger:     logger,
		tracer:     tracer,
		baseSleep:  100 * time.Millisecond,
		maxSleep:   10 * time.Second,
	}
}

// Run starts the pull loop. It blocks until the context is canceled.
func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info("agent starting pull loop")

	retries := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Using Fetch(1) for demonstration. In a real system, you might fetch batches.
			// Or better yet, Consume() for continuous async delivery.
			// The specification asked for "NATS Consumer Pull mechanics" and "exponential backoff".
			// JetStream's Fetch blocks for a specified time or until messages arrive.
			msgs, err := a.consumer.FetchNoWait(1)
			if err != nil {
				// No messages or connection error. Apply backoff.
				sleepDuration := a.calculateBackoff(retries)
				a.logger.Debug("fetch error or empty, backing off", slog.String("error", err.Error()), slog.Duration("sleep", sleepDuration))

				select {
				case <-time.After(sleepDuration):
				case <-ctx.Done():
					return ctx.Err()
				}

				retries++
				continue
			}

			// We got messages. Reset retries.
			retries = 0

			for msg := range msgs.Messages() {
				a.handleMessage(ctx, msg)
			}
			if msgs.Error() != nil {
				a.logger.Error("message stream error", slog.String("error", msgs.Error().Error()))
			}
		}
	}
}

func (a *Agent) handleMessage(ctx context.Context, msg jetstream.Msg) {
	// The far end of PLAN.md Section 19's "API Request -> Event Bus ->
	// Runner Execution" trace. The publishing Bus left W3C trace context
	// in the message headers (internal/event/trace.go); extracting it here
	// makes this span a child of the API request's span, in the same
	// trace, rather than the root of a second one that no operator could
	// connect back to the request that caused it.
	ctx = event.ExtractTraceContext(ctx, msg.Headers())
	ctx, span := a.tracer.Start(ctx, "runner.handle_message",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination.name", msg.Subject())),
	)
	defer span.End()

	// Every publish in this codebase goes through Bus.Publish now, which
	// wraps the domain payload in the Event envelope (internal/event) --
	// so the raw bytes on the wire are a marshaled Event, not a bare
	// DispatchPayload. natsBus.Subscribe callers get this unwrapped for
	// them automatically; Agent, which deliberately stays on its own pull
	// loop instead of going through Bus.Subscribe, has to do the same
	// two-step unwrap itself: decode the envelope, then decode
	// DispatchPayload out of its Data field.
	var evt event.Event
	if err := json.Unmarshal(msg.Data(), &evt); err != nil {
		a.logger.Error("dropping malformed message", slog.String("error", err.Error()))
		if err := msg.Term(); err != nil {
			a.logger.Error("failed to terminate malformed message", slog.String("error", err.Error()))
		}
		return
	}

	var payload wire.DispatchPayload
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		a.logger.Error("dropping malformed message", slog.String("error", err.Error()))
		// Terminated, not Ack'd: a permanently malformed payload will
		// never become parseable no matter how many times it is
		// redelivered, so Ack (which would silently discard it with no
		// trace) and Nak (which would retry it forever) are both wrong.
		// Term matches natsBus.Subscribe's own identical handling of a
		// decode failure (internal/event/consumer.go).
		if err := msg.Term(); err != nil {
			a.logger.Error("failed to terminate malformed message", slog.String("error", err.Error()))
		}
		return
	}

	span.SetAttributes(
		attribute.String("pleiades.job.id", payload.JobID),
		attribute.String("pleiades.runbook.id", payload.RunbookID),
		attribute.String("pleiades.device.name", payload.DeviceName),
	)
	a.logger.Info("processing job",
		slog.String("runbook", payload.RunbookID),
		slog.String("device", payload.DeviceName),
	)

	// Execute the Runbook using the configured adapter.
	if err := a.adapter.Execute(ctx, payload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "adapter execution failed")
		a.logger.Error("adapter execution failed", slog.String("error", err.Error()))
		// Routed through the same Dead Letter Queue mechanism
		// natsBus.Subscribe itself uses (event.HandleDeliveryFailure),
		// rather than the previous "log it and ack" behavior, which
		// silently discarded every failed job on its very first attempt
		// with no operator-visible trail (PATTERNS.md's own Dead Letter
		// Queue entry named this exact line as the gap). Below
		// a.maxDeliver attempts this Naks with backoff for redelivery;
		// at or beyond it, the job is dead-lettered and terminated.
		if dlqErr := event.HandleDeliveryFailure(ctx, a.js, msg, a.maxDeliver, err.Error()); dlqErr != nil {
			a.logger.Error("failed to route failed delivery to dead letter handling", slog.String("error", dlqErr.Error()))
		}
		return
	}

	// Acknowledge the message as complete
	if err := msg.Ack(); err != nil {
		a.logger.Error("failed to ack successfully handled message", slog.String("error", err.Error()))
	}
}

// calculateBackoff computes how long to sleep before the next poll
// retry, using the shared jittered exponential backoff formula in
// pkg/retry (see retry.Backoff's doc comment for the algorithm and why
// jitter matters). This method is kept, rather than calling
// retry.Backoff directly at the one call site in Run, so a.baseSleep and
// a.maxSleep stay the only things a caller needs to know about; it is a
// pure delegation with no behavior of its own.
func (a *Agent) calculateBackoff(retries int) time.Duration {
	return retry.Backoff(a.baseSleep, a.maxSleep, retries)
}
