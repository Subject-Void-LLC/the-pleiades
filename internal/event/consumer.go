package event

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go/jetstream"
)

// Subscribe creates (or rejoins) a durable named consumer for topic and
// invokes handler for each delivery.
//
// The consumer is durable and named deterministically from topic
// (topology.SubscribeConsumerConfig/DurableName), not the anonymous
// ephemeral consumer this adapter used before: every caller that
// Subscribes to the same topic joins the same consumer group, so
// JetStream itself guarantees exactly one of them receives a given message
// (PLAN.md Section 26.4's split-brain mitigation for horizontally-scaled,
// stateless controllers), and a restart resumes from the consumer's own ack
// floor instead of replaying or skipping.
//
// The jetstream.ConsumeContext returned by Consume is captured (previously
// discarded, which meant ctx cancellation had no effect and the consuming
// goroutine could never be stopped): a goroutine watches ctx.Done() and
// calls cc.Stop() then waits on cc.Closed(), mirroring the pattern
// internal/api/logs.go already uses for the same problem.
//
// A panic inside handler is recovered, logged, and routed through the same
// failure path as an ordinary handler error (HandleDeliveryFailure), never
// allowed to kill the process. A message that fails to unmarshal as an
// Event is terminated (Term), not negatively acknowledged: NATS redelivers
// a Nak'd message, and a permanently malformed payload would never become
// parseable no matter how many times it is retried.
func (b *natsBus) Subscribe(ctx context.Context, topic string, handler func(Event) error) error {
	durable := topology.DurableName(topic)
	consumer, err := b.js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.SubscribeConsumerConfig(durable, topic))
	if err != nil {
		return fmt.Errorf("failed to create consumer for %s: %w", topic, err)
	}

	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		b.handleDelivery(ctx, msg, handler)
	})
	if err != nil {
		return fmt.Errorf("failed to start consumer for %s: %w", topic, err)
	}

	go func() {
		<-ctx.Done()
		cc.Stop()
		<-cc.Closed()
	}()

	return nil
}

// handleDelivery decodes msg, invokes handler with panic recovery, and
// routes the outcome to Ack (success) or HandleDeliveryFailure (decode
// failure, handler error, or recovered panic). It exists as its own method,
// rather than an inline closure, so the deferred recover can name a msg
// that is already in scope by the time the panic unwinds through it.
func (b *natsBus) handleDelivery(ctx context.Context, msg jetstream.Msg, handler func(Event) error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered panic in event handler",
				slog.String("subject", msg.Subject()),
				slog.Any("panic", r),
			)
			if err := HandleDeliveryFailure(ctx, b.js, msg, topology.MaxDeliverDefault, fmt.Sprintf("handler panic: %v", r)); err != nil {
				slog.Error("failed to route panicking delivery to dead letter handling",
					slog.String("subject", msg.Subject()),
					slog.String("error", err.Error()),
				)
			}
		}
	}()

	var evt Event
	if err := json.Unmarshal(msg.Data(), &evt); err != nil {
		slog.Error("dropping malformed event payload",
			slog.String("subject", msg.Subject()),
			slog.String("error", err.Error()),
		)
		if termErr := msg.Term(); termErr != nil {
			slog.Error("failed to terminate malformed message",
				slog.String("subject", msg.Subject()),
				slog.String("error", termErr.Error()),
			)
		}
		return
	}

	if err := handler(evt); err != nil {
		if dlqErr := HandleDeliveryFailure(ctx, b.js, msg, topology.MaxDeliverDefault, err.Error()); dlqErr != nil {
			slog.Error("failed to route failed delivery to dead letter handling",
				slog.String("subject", msg.Subject()),
				slog.String("error", dlqErr.Error()),
			)
		}
		return
	}

	if err := msg.Ack(); err != nil {
		slog.Error("failed to ack successfully handled message",
			slog.String("subject", msg.Subject()),
			slog.String("error", err.Error()),
		)
	}
}
