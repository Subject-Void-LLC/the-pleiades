// Package journal_test: the attempt context value.
package journal_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

func TestAttemptRoundTrips(t *testing.T) {
	ctx := journal.WithAttempt(context.Background(), 3)
	got, ok := journal.AttemptFrom(ctx)
	if !ok {
		t.Fatal("AttemptFrom did not find the value WithAttempt stored")
	}
	if got != 3 {
		t.Errorf("AttemptFrom = %d, want 3", got)
	}
}

func TestAttemptFromDistinguishesZeroFromAbsent(t *testing.T) {
	// The bool is the whole point. Zero is a real value with its own
	// meaning: JournalEntry documents Attempt as zero on the Crawl tier,
	// which has no dispatch at all. A sink that read a missing value as
	// zero would record a retried run as if it came from a tier that
	// cannot retry.
	if _, ok := journal.AttemptFrom(context.Background()); ok {
		t.Error("AttemptFrom reported a value on a context that carries none")
	}

	stored, ok := journal.AttemptFrom(journal.WithAttempt(context.Background(), 0))
	if !ok {
		t.Error("AttemptFrom reported no value for a stored zero")
	}
	if stored != 0 {
		t.Errorf("AttemptFrom = %d, want 0", stored)
	}
}

func TestAttemptSurvivesADetachedContext(t *testing.T) {
	// The value crosses two detachments between the Runner and the sink:
	// the Runner's own lease context and the engine's WithoutCancel.
	// Both keep values and drop only cancellation, which is the property
	// that makes a context value the right carrier here.
	base := journal.WithAttempt(context.Background(), 5)
	cancellable, cancel := context.WithCancel(base)
	cancel()

	detached := context.WithoutCancel(cancellable)
	if err := detached.Err(); err != nil {
		t.Fatalf("WithoutCancel kept the cancellation: %v", err)
	}
	got, ok := journal.AttemptFrom(detached)
	if !ok || got != 5 {
		t.Errorf("AttemptFrom after detachment = (%d, %v), want (5, true)", got, ok)
	}
}

func TestAttemptKeyCannotCollideWithAnotherPackage(t *testing.T) {
	// The key is a private type, so a bare int or string key defined
	// elsewhere cannot read or overwrite it even at the same numeric
	// value.
	ctx := context.WithValue(context.Background(), "attempt", 99) //nolint:staticcheck // a deliberately bad key, which is the point
	if _, ok := journal.AttemptFrom(ctx); ok {
		t.Error("a string key reached this package's value")
	}

	ctx = journal.WithAttempt(ctx, 7)
	if got := ctx.Value("attempt"); got != 99 { //nolint:staticcheck // reading back the deliberately bad key
		t.Errorf("WithAttempt overwrote another package's value: %v", got)
	}
}
