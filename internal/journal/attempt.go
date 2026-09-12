// Package journal: how a dispatch's redelivery count reaches the sink.
//
// JournalEntry.Attempt is JetStream's own redelivery counter, so a second
// run against one device reads as a retry rather than as two unrelated
// runs. The number exists in exactly one place, on the jetstream.Msg the
// Runner pulled, and the sink is built three layers below that.
//
// Two ways across were possible. Widening runner.ExecutionAdapter and
// routing.Executor together would put a journal concern into two
// interfaces that have nothing to do with journaling, and every
// implementation of both would have to grow a parameter it ignores. A
// context value crosses the same distance without changing a signature,
// and survives the trip: the Runner's own detached context delegates
// Value to its parent and drops only cancellation, and the engine hands
// the sink context.WithoutCancel, which also keeps values.
//
// The key lives here rather than in internal/runner because both ends
// have to reach it and internal/runner is one of the ends. It belongs to
// the journal in any case: the attempt number is carried for no other
// reason.
package journal

import "context"

// ctxKey is a private type so this package's context keys can never
// collide with a key defined by another package, even one that also uses
// a bare int or string as its key type.
type ctxKey int

// ctxKeyAttempt is the dispatch redelivery counter's key.
const ctxKeyAttempt ctxKey = iota

// WithAttempt returns a context carrying a dispatch's redelivery count.
//
// The Runner sets this once per delivery, before it hands the payload to
// an adapter. Nothing else should: a second setter would mean two
// answers to "which attempt is this" with no way to tell which won.
func WithAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, ctxKeyAttempt, attempt)
}

// AttemptFrom returns the redelivery count stored in ctx, and whether
// one was there.
//
// The bool matters and should not be dropped. Zero is a real value with
// its own meaning: JournalEntry documents Attempt as zero on the Crawl
// tier, which has no dispatch at all. A Walk-tier sink that read a
// missing value as zero would record a retried run as if it came from a
// tier that cannot retry.
func AttemptFrom(ctx context.Context) (int, bool) {
	attempt, ok := ctx.Value(ctxKeyAttempt).(int)
	return attempt, ok
}
