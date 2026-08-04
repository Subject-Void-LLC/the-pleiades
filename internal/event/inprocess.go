package event

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// subjectMatches reports whether topic matches pattern using NATS's trailing
// wildcard convention, the only wildcard form this codebase's one real
// caller (NewNatsBus, which creates its stream on subject
// "pleiades.>" via topology.StreamConfig) actually demonstrates.
//
// A pattern whose final dot-delimited token is the literal ">" matches any
// topic that shares every leading token with the pattern and supplies at
// least one additional token beyond that prefix. For example,
// "pleiades.events.>" matches "pleiades.events.foo" and
// "pleiades.events.foo.bar", but not "pleiades.events" alone, since ">"
// requires at least one more token. A pattern without a trailing ">"
// requires an exact string match.
//
// This function intentionally supports only the trailing ">" wildcard, not
// NATS's single-token "*" wildcard. Implementing "*" would be guessing at
// behavior with no caller in this codebase to model it against, so that
// form is left out rather than silently under-implemented.
func subjectMatches(pattern, topic string) bool {
	// Fast path: exact match covers the no-wildcard case and is also
	// correct when pattern and topic happen to be identical strings that
	// end in ">".
	if pattern == topic {
		return true
	}

	patternTokens := strings.Split(pattern, ".")
	lastToken := patternTokens[len(patternTokens)-1]
	if lastToken != ">" {
		// No trailing wildcard token: only an exact match qualifies, and
		// that was already ruled out above.
		return false
	}

	// The tokens before the trailing ">" must match the topic's leading
	// tokens one for one.
	prefixTokens := patternTokens[:len(patternTokens)-1]
	topicTokens := strings.Split(topic, ".")

	// ">" requires at least one token beyond the prefix, so a topic with no
	// extra tokens (or fewer tokens than the prefix) never matches.
	if len(topicTokens) <= len(prefixTokens) {
		return false
	}

	for i, prefixToken := range prefixTokens {
		if prefixToken != topicTokens[i] {
			return false
		}
	}
	return true
}

// subscription pairs a subject pattern with the handler registered for it.
type subscription struct {
	// pattern is the topic string passed to Subscribe, interpreted with
	// subjectMatches's trailing ">" wildcard convention.
	pattern string
	// handler is invoked once per delivery for a matching Publish.
	handler func(Event) error
}

// inProcessBus is an in-memory, single-process implementation of Bus. It
// keeps every subscription in a slice guarded by mu and never touches the
// network or disk, making it useful for tests and for any deployment that
// does not need durable, cross-process delivery.
//
// It does not implement redelivery, negative acknowledgement, or the Dead
// Letter Queue: a handler's returned error is logged and otherwise has no
// effect, since there is no broker-side redelivery mechanism underneath it
// to drive. This is a real, permanent adapter limitation, stated here
// rather than left implicit; a caller that needs at-least-once semantics
// with retry uses NewNatsBus.
type inProcessBus struct {
	// mu guards subs against concurrent Publish and Subscribe calls.
	mu sync.Mutex
	// subs holds every registered subscription in registration order.
	subs []subscription
}

// NewInProcessBus constructs a Bus backed by an in-process registry of
// subscriptions instead of an external broker. It is substitutable
// anywhere a Bus is expected, matching natsBus's Publish and Subscribe
// contracts (fire-and-forget publish, envelope stamped from context, no
// wire-format decode failures possible since delivery is a direct in-memory
// call) so callers cannot tell the two adapters apart by behavior alone,
// short of the redelivery/DLQ limitation documented on inProcessBus itself.
func NewInProcessBus() Bus {
	return &inProcessBus{}
}

// Publish stamps evt's envelope fields from ctx (identically to
// natsBus.Publish; see stampEnvelope) and fires it at every subscription
// whose pattern matches topic. It returns as soon as the matching
// subscriptions are identified; it does not wait for any handler to run.
// Each matching handler is invoked on its own goroutine, mirroring how
// natsBus already delivers to handlers asynchronously relative to the
// publisher, so a slow or blocking handler never delays this call or other
// subscribers.
func (b *inProcessBus) Publish(ctx context.Context, topic string, evt Event) error {
	// Honor cancellation up front. There is no long-lived work below this
	// point to select on, so a single check at entry is sufficient.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("in-process bus publish canceled: %w", err)
	}

	stampEnvelope(ctx, &evt)

	// Snapshot the matching handlers while holding the lock, then release
	// it before dispatching. Holding the lock across handler invocation
	// would let one slow handler block every other Publish and Subscribe
	// call on the bus.
	b.mu.Lock()
	matched := make([]func(Event) error, 0, len(b.subs))
	for _, sub := range b.subs {
		if subjectMatches(sub.pattern, topic) {
			matched = append(matched, sub.handler)
		}
	}
	b.mu.Unlock()

	for _, handler := range matched {
		go deliver(topic, evt, handler)
	}
	return nil
}

// deliver invokes handler with evt. If handler returns an error, it is
// logged: see inProcessBus's own doc comment for why that is the extent of
// this adapter's failure handling.
func deliver(topic string, evt Event, handler func(Event) error) {
	if err := handler(evt); err != nil {
		slog.Error("in-process event handler returned an error",
			slog.String("topic", topic),
			slog.String("event_id", evt.ID),
			slog.String("error", err.Error()),
		)
	}
}

// Subscribe registers handler to receive every future Publish whose topic
// matches the given pattern under subjectMatches's trailing ">" wildcard
// convention. It returns nil as soon as the subscription is recorded, not
// when the subscription later stops, matching natsBus.Subscribe's own
// contract. Multiple subscriptions on overlapping patterns each receive
// their own delivery for a matching Publish: unlike natsBus, which groups
// same-topic Subscribe calls into one shared durable consumer (PLAN.md
// Section 26.4), the in-process adapter has no consumer-group concept and
// always fans out to every registered handler.
func (b *inProcessBus) Subscribe(ctx context.Context, topic string, handler func(event Event) error) error {
	// Honor cancellation up front, matching Publish's behavior.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("in-process bus subscribe canceled: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, subscription{pattern: topic, handler: handler})
	return nil
}

// Close is a no-op: inProcessBus holds no network connection or other
// resource that needs releasing, only in-memory subscriptions that are
// garbage collected with the bus itself.
func (b *inProcessBus) Close() error {
	return nil
}
