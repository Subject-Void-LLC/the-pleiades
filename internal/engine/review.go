// The Crawl tier's sdk.Reviewer: a result an ai method holds for a person
// is asked about at the terminal when someone is there, and otherwise kept
// pending in the project (internal/aireview) for `pleiades ai review`.
package engine

import (
	"context"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/aireview"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ReviewPrompter asks a person at the terminal to decide a held result.
// The composition root passes one only when a person can be asked: its
// standard input is a terminal and no agent drives it. With none, every
// held result is kept pending.
type ReviewPrompter interface {
	// Ask shows r and returns whether the person approved it, who they
	// are, and their comment. An error means nobody decided, and the
	// review is kept pending.
	Ask(ctx context.Context, r sdk.Review) (approve bool, by, comment string, err error)
}

// reviewDesk is where a run's held results go: one person asked at a
// time, however many devices hold at once.
type reviewDesk struct {
	prompter ReviewPrompter
	mu       sync.Mutex
}

// WithReviewPrompter lets the run ask the person at its terminal to
// decide a result an ai method holds, rather than keeping it pending. It
// takes effect with WithAIProject, whose project keeps the decision.
func WithReviewPrompter(p ReviewPrompter) CollectionActionExecutorOption {
	return func(e *collectionActionExecutor) {
		e.reviews = &reviewDesk{prompter: p}
	}
}

// Decided implements sdk.Reviewer.
func (c *runbookContext) Decided(key string) (sdk.Review, bool, error) {
	if c.ai == nil {
		return sdk.Review{}, false, sdk.ErrNoAIProject
	}
	return aireview.Load(c.ai.dir, key)
}

// Hold implements sdk.Reviewer. A person at the terminal decides now, and
// the decision is recorded so a run asking the same thing again reuses
// it; with nobody there, or a prompt that ends undecided, the review is
// recorded pending.
//
// Phase 27 replaces the pending branch: rather than the task failing as
// held and the run being started again, the run would pause here
// (internal/engine's pause.go) and resume when the decision arrives.
func (c *runbookContext) Hold(ctx context.Context, r sdk.Review) (sdk.Review, error) {
	if c.ai == nil {
		return sdk.Review{}, sdk.ErrNoAIProject
	}
	r.Status, r.HeldAt = sdk.ReviewPending, time.Now().UTC().Format(time.RFC3339)
	r.AnswerSHA256 = aireview.AnswerSHA256(r.Answer)
	if desk := c.reviews; desk != nil && desk.prompter != nil {
		desk.mu.Lock()
		defer desk.mu.Unlock()
		if approve, by, comment, err := desk.prompter.Ask(ctx, r); err == nil {
			r.Status = sdk.ReviewDenied
			if approve {
				r.Status = sdk.ReviewApproved
			}
			r.DecidedBy, r.DecidedAt, r.Comment, r.DecidedSHA256 = by, time.Now().UTC().Format(time.RFC3339), comment, r.AnswerSHA256
			return r, aireview.Record(c.ai.dir, r)
		}
	}
	return aireview.Create(c.ai.dir, r)
}
