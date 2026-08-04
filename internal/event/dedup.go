package event

import (
	"context"
	"fmt"
	"time"
)

// DefaultIdempotencyKeyDerivation returns evt.ID as the default idempotency
// key.
//
// An earlier version of this function hashed topic plus evt.Data instead,
// on the reasoning that ID is "freshly minted" and therefore unstable. That
// reasoning was backwards, and a real NATS container test caught it: ID is
// minted exactly once, by WrapPayload, and stays fixed on that Event value
// for the rest of its life, including across a caller-side retry that
// resends the same already-built Event (the actual shape "a retry of the
// same logical operation," PLAN.md Section 26.2, takes in this codebase --
// nothing re-calls WrapPayload mid-retry). Hashing topic+Data instead meant
// two entirely different events that happened to share a topic and payload
// content (e.g. two identical health-check pings a minute apart) collided
// on the same key, and JetStream's producer-side dedup window silently
// swallowed the second one as if it were a duplicate of the first. Keying
// on ID fixes this: stable across a retry of one event, unique across any
// two distinct ones, content notwithstanding.
//
// A caller that knows a more meaningful natural key than a random ID
// (e.g. Dispatcher's JobID+DeviceID pair, stable across that dispatch's own
// retries and meaningful across process restarts in a way a fresh random ID
// is not) overrides this default via WithIdempotencyKey.
func DefaultIdempotencyKeyDerivation(evt Event) string {
	return evt.ID
}

// DedupStore is the port behind the Idempotent Consumer decorator
// (NewIdempotentBus): a shared seen-set with a time to live, per PLAN.md
// Section 26.2, so no individual consumer writes its own deduplication
// logic. dedup_inprocess.go and dedup_nats.go are its two adapters.
type DedupStore interface {
	// SeenRecently reports whether key was marked seen within its TTL.
	SeenRecently(ctx context.Context, key string) (bool, error)
	// MarkSeen records key as seen, expiring after ttl.
	MarkSeen(ctx context.Context, key string, ttl time.Duration) error
}

// idempotentBus decorates an inner Bus so that Subscribe's handler is only
// ever invoked once per distinct IdempotencyKey within store's TTL, even
// though the underlying transport may redeliver the same message more than
// once.
type idempotentBus struct {
	inner Bus
	store DedupStore
	ttl   time.Duration
}

// NewIdempotentBus wraps inner with the Idempotent Consumer decorator
// (PLAN.md Section 26.2). Publish is untouched: it is delegated to inner
// unchanged, since Publish is already what stamps IdempotencyKey onto the
// outgoing Event (see natsBus.Publish/inProcessBus.Publish) and engages
// JetStream's own producer-side dedup window for the natsBus case.
// Subscribe wraps the caller's handler with a check-then-call-then-mark
// sequence, in exactly that order:
//
//  1. Check whether evt.IdempotencyKey was seen recently. If so, return nil
//     without ever calling the real handler: the inner Bus acknowledges
//     this delivery as a successful no-op.
//  2. Otherwise call the real handler.
//  3. Mark the key seen only if the handler returned nil.
//  4. On a handler error, propagate it unchanged and do NOT mark the key
//     seen.
//
// This ordering is deliberate and load-bearing: marking a key seen before
// calling the handler would make a first-attempt failure indistinguishable
// from a completed success on redelivery, since the decorator would see
// "already seen" on the retry and skip the handler entirely, permanently
// swallowing the failure before it could ever reach HandleDeliveryFailure's
// exhaustion/DLQ path. Marking seen only after a nil return keeps
// at-least-once delivery and the Dead Letter Queue both intact for a
// handler that fails one or more times before eventually succeeding, or
// never succeeds at all.
func NewIdempotentBus(inner Bus, store DedupStore, ttl time.Duration) Bus {
	return &idempotentBus{inner: inner, store: store, ttl: ttl}
}

func (b *idempotentBus) Publish(ctx context.Context, topic string, evt Event) error {
	return b.inner.Publish(ctx, topic, evt)
}

func (b *idempotentBus) Subscribe(ctx context.Context, topic string, handler func(Event) error) error {
	wrapped := func(evt Event) error {
		if evt.IdempotencyKey != "" {
			seen, err := b.store.SeenRecently(ctx, evt.IdempotencyKey)
			if err != nil {
				return fmt.Errorf("dedup store lookup failed for %s: %w", evt.IdempotencyKey, err)
			}
			if seen {
				return nil
			}
		}

		if err := handler(evt); err != nil {
			return err
		}

		if evt.IdempotencyKey != "" {
			if err := b.store.MarkSeen(ctx, evt.IdempotencyKey, b.ttl); err != nil {
				return fmt.Errorf("dedup store mark-seen failed for %s: %w", evt.IdempotencyKey, err)
			}
		}
		return nil
	}
	return b.inner.Subscribe(ctx, topic, wrapped)
}

// Close delegates to the inner Bus. The dedup store itself (DedupStore)
// holds no connection of its own the decorator would need to release
// separately: natsDedupStore reuses the same JetStream handle natsBus
// already manages, and inProcessDedupStore is a plain in-memory map.
func (b *idempotentBus) Close() error {
	return b.inner.Close()
}
