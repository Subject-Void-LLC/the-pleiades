package event

import "context"

// ctxKey is a private type so this package's context keys can never
// collide with a key defined by another package, even one that also uses a
// bare string as its key type.
type ctxKey int

const (
	ctxKeyCorrelationID ctxKey = iota
	ctxKeyCausationID
	ctxKeyChainDepth
	ctxKeyActor
	ctxKeyTraceID
	ctxKeyIdempotencyKey
)

// WithCorrelationID returns a context carrying correlationID, so a
// subsequent Bus.Publish stamps it onto the outgoing Event without the
// caller needing to touch the Event struct directly.
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, ctxKeyCorrelationID, correlationID)
}

// CorrelationIDFromContext returns the correlation ID stored in ctx, if
// any.
func CorrelationIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyCorrelationID).(string)
	return v, ok
}

// WithCausationID returns a context carrying causationID, so a subsequent
// Bus.Publish stamps it onto the outgoing Event.
func WithCausationID(ctx context.Context, causationID string) context.Context {
	return context.WithValue(ctx, ctxKeyCausationID, causationID)
}

// CausationIDFromContext returns the causation ID stored in ctx, if any.
func CausationIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyCausationID).(string)
	return v, ok
}

// WithChainDepth returns a context carrying depth, so a subsequent
// Bus.Publish stamps it onto the outgoing Event.
func WithChainDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, ctxKeyChainDepth, depth)
}

// ChainDepthFromContext returns the chain depth stored in ctx, if any.
func ChainDepthFromContext(ctx context.Context) (int, bool) {
	v, ok := ctx.Value(ctxKeyChainDepth).(int)
	return v, ok
}

// WithActor returns a context carrying actor (a human identity subject or
// service account name), so a subsequent Bus.Publish stamps it onto the
// outgoing Event.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, ctxKeyActor, actor)
}

// ActorFromContext returns the actor stored in ctx, if any.
func ActorFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyActor).(string)
	return v, ok
}

// WithTraceID returns a context carrying traceID, so a subsequent
// Bus.Publish stamps it onto the outgoing Event. Callers that already have
// their own trace ID context convention (e.g. internal/api's
// TraceIDMiddleware) bridge it in here explicitly rather than this package
// reaching into another package's unexported context keys.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ctxKeyTraceID, traceID)
}

// TraceIDFromContext returns the trace ID stored in ctx, if any.
func TraceIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyTraceID).(string)
	return v, ok
}

// WithIdempotencyKey returns a context carrying a caller-supplied
// idempotency key, so a subsequent Bus.Publish uses it instead of deriving
// a default one. Use this when the caller already owns a stable identity
// for the logical operation being published (e.g. a dispatch's
// JobID+DeviceID pair) that is more precise than the generic
// topic-plus-payload-hash default.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, ctxKeyIdempotencyKey, key)
}

// IdempotencyKeyFromContext returns the idempotency key stored in ctx, if
// any.
func IdempotencyKeyFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyIdempotencyKey).(string)
	return v, ok
}

// Chained returns a context seeded from received so that a Publish made
// with it continues received's causal chain: CorrelationID carries forward
// unchanged (or, if received is itself a chain root with no
// CorrelationID, received.ID becomes the new chain's root), CausationID
// becomes received.ID, and ChainDepth becomes received.ChainDepth+1.
//
// This is the wire-level mechanism PLAN.md Section 15's loop prevention
// needs (a chain root and a depth counter that travel with every hop), not
// the enforcement of it: nothing in this package reads ChainDepth back and
// halts a chain that has gone too deep, because the Trigger Engine that
// would own that decision does not exist as a package yet. A future caller
// wires the ceiling check on top of the depth this function already
// propagates.
func Chained(parent context.Context, received Event) context.Context {
	correlationID := received.CorrelationID
	if correlationID == "" {
		correlationID = received.ID
	}
	ctx := WithCorrelationID(parent, correlationID)
	ctx = WithCausationID(ctx, received.ID)
	ctx = WithChainDepth(ctx, received.ChainDepth+1)
	return ctx
}
