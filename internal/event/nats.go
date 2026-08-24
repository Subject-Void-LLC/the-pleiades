package event

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type natsBus struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// NewNatsBus connects to an external NATS broker and binds the single
// Pleiades stream according to role.
// It adheres to the Liskov Substitution Principle by perfectly substituting the event.Bus interface.
//
// topology.Connect is the module's one way to obtain a NATS connection:
// it dials with the shared options and returns only once the connection
// is actually usable, which matters here because jetstream.New and the
// stream binding below both talk to the server immediately. Once
// connected, the bus survives an outage of any length rather than dying
// permanently after about two minutes, which is what it did before
// Phase 96a.
//
// role is required rather than defaulted because it decides whether this
// process may CHANGE the shape of a stream other processes are already
// using. Only cmd/controller passes topology.StreamProvisioner; every
// other caller passes topology.StreamReader and therefore cannot revert
// an operator's retention choice, which is FAILURE_PATTERNS.md #178. A
// reader still creates the stream when it is absent, so no start ordering
// between the Controller and a Runner is introduced.
//
// logger may be nil, in which case slog.Default() is used. Production
// callers pass the composition root's own logger so the connection
// lifecycle events reach the same masked handler everything else does.
func NewNatsBus(ctx context.Context, url string, logger *slog.Logger, role topology.StreamRole, budget topology.OutageBudget, allowDiscard bool) (Bus, error) {
	nc, err := topology.Connect(ctx, url, logger, "event-bus")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to nats at %s: %w", url, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to initialize jetstream: %w", err)
	}

	_, drift, err := topology.BindStream(ctx, js, role, budget, allowDiscard)
	if err != nil {
		nc.Close()
		return nil, err
	}
	// Drift is a warning, never a refusal. During a mixed rollout an old
	// binary is no longer reverting the shape, but it is also not
	// applying the new one, and this log line is the only thing in the
	// system that would say so.
	if len(drift) > 0 {
		fields := make([]string, 0, len(drift))
		for _, d := range drift {
			fields = append(fields, d.String())
		}
		log := logger
		if log == nil {
			log = slog.Default()
		}
		// The budget is named because it is the most likely cause: two
		// binaries given different PLEIADES_MAX_OUTAGE values derive
		// different MaxAge and Duplicates, and without this an operator
		// who set it on one Deployment and not the other reads a warning
		// that points at the wrong thing.
		log.Warn("the live stream shape differs from what this build declares",
			"role", role.String(), "outage_budget", budget.String(),
			"likely_cause", "PLEIADES_MAX_OUTAGE differs between processes, or an upgrade is mid-rollout",
			"drift", strings.Join(fields, "; "))
	}

	return &natsBus{
		nc: nc,
		js: js,
	}, nil
}

// Publish stamps evt's envelope fields from ctx, then fires it into the
// NATS mesh as JSON.
//
// CorrelationID gets a fresh UUID if ctx did not carry one, making this
// publish the root of a new causal chain; CausationID, ChainDepth, Actor,
// and TraceID come from ctx if present and stay zero-value otherwise (see
// WithCorrelationID and its siblings, and Chained for continuing an
// existing chain).
//
// IdempotencyKey comes from ctx if the caller set one (see
// WithIdempotencyKey), otherwise it defaults to evt.ID (see
// DefaultIdempotencyKeyDerivation) so a caller-side retry that resends the
// same already-built Event is recognizable even without explicit opt-in,
// while two distinct events never collide merely for sharing the same
// content. The key is both stored on the envelope (for the Idempotent
// Consumer decorator, NewIdempotentBus) and passed as jetstream.WithMsgID,
// engaging JetStream's own producer-side dedup window: the two mechanisms
// catch different failure modes (a duplicate publish vs. a duplicate
// delivery of one already-accepted publish).
func (b *natsBus) Publish(ctx context.Context, topic string, evt Event) error {
	stampEnvelope(ctx, &evt)

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal event for %s: %w", topic, err)
	}

	// The W3C trace context rides in the message headers rather than the
	// JSON body: out of band from the payload, so a consumer can decide
	// whether to continue the trace before it has parsed (or failed to
	// parse) anything, and readable by a non-Go consumer that knows
	// nothing about this Event schema. See trace.go.
	msg := &nats.Msg{Subject: topic, Data: data, Header: nats.Header{}}
	InjectTraceContext(ctx, msg.Header)

	if _, err := b.js.PublishMsg(ctx, msg, jetstream.WithMsgID(evt.IdempotencyKey)); err != nil {
		return fmt.Errorf("failed to publish to %s: %w", topic, err)
	}
	return nil
}

// stampEnvelope fills in evt's envelope fields from ctx in place, applying
// Publish's own documented defaulting rules. It is a free function, not a
// natsBus method, because inProcessBus.Publish needs the identical logic
// and there is exactly one implementation of it, per PLAN.md Section 25's
// Build-Once rule.
func stampEnvelope(ctx context.Context, evt *Event) {
	if correlationID, ok := CorrelationIDFromContext(ctx); ok && correlationID != "" {
		evt.CorrelationID = correlationID
	} else {
		evt.CorrelationID = uuid.New().String()
	}
	if causationID, ok := CausationIDFromContext(ctx); ok {
		evt.CausationID = causationID
	}
	if depth, ok := ChainDepthFromContext(ctx); ok {
		evt.ChainDepth = depth
	}
	if actor, ok := ActorFromContext(ctx); ok {
		evt.Actor = actor
	}
	if traceID, ok := TraceIDFromContext(ctx); ok {
		evt.TraceID = traceID
	}

	if key, ok := IdempotencyKeyFromContext(ctx); ok && key != "" {
		evt.IdempotencyKey = key
	} else {
		evt.IdempotencyKey = DefaultIdempotencyKeyDerivation(*evt)
	}
}

// Close gracefully drains the NATS connection: already-received messages
// finish processing and already-queued publishes flush before the
// connection actually closes, rather than dropping them (PATTERNS.md's
// Graceful Shutdown entry). The error is returned, not swallowed, so a
// caller's own shutdown path can log a drain that failed to complete
// cleanly instead of silently proceeding as if it had.
func (b *natsBus) Close() error {
	return b.nc.Drain()
}
