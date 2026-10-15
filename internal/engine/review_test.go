// Tests for the Crawl tier's sdk.Reviewer and the pause declarations.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/aireview"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// prompter decides every review the same way, counting how many people
// were asked at once.
type prompter struct {
	approve      bool
	err          error
	mu           sync.Mutex
	asking, most int
}

func (p *prompter) Ask(_ context.Context, r sdk.Review) (bool, string, string, error) {
	p.mu.Lock()
	p.asking++
	if p.asking > p.most {
		p.most = p.asking
	}
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.asking--; p.mu.Unlock() }()
	return p.approve, "ops", "looked", p.err
}

func reviewContext(t *testing.T, p ReviewPrompter) (*runbookContext, string) {
	t.Helper()
	dir := t.TempDir()
	e := &collectionActionExecutor{}
	WithAIProject(dir, nil, nil)(e)
	if p != nil {
		WithReviewPrompter(p)(e)
	}
	rc := NewRunbookContext(nil).(*runbookContext)
	e.lendAIProject(rc)
	return rc, dir
}

func review(n int) sdk.Review {
	return sdk.Review{Key: fmt.Sprintf("%064x", n), Method: "ai.chat", Answer: "answer", Reasons: []string{"always"}}
}

// TestHold_AskedAtTheTerminal: a person approves; the decision is kept
// so the same request asked again finds it; and however many devices hold
// at once, one person is asked at a time.
func TestHold_AskedAtTheTerminal(t *testing.T) {
	p := &prompter{approve: true}
	rc, dir := reviewContext(t, p)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := rc.Hold(context.Background(), review(i)); err != nil || r.Status != sdk.ReviewApproved {
				t.Errorf("hold %d: %+v %v", i, r, err)
			}
		}()
	}
	wg.Wait()
	if p.most != 1 {
		t.Errorf("%d people were asked at once", p.most)
	}
	if r, ok, err := aireview.Load(dir, review(3).Key); !ok || err != nil || r.Status != sdk.ReviewApproved || r.DecidedBy != "ops" {
		t.Errorf("kept: %+v %v %v", r, ok, err)
	}
	if r, ok, _ := rc.Decided(review(3).Key); !ok || r.Status != sdk.ReviewApproved {
		t.Errorf("decided: %+v", r)
	}
}

// TestHold_NobodyThereIsPending: with no prompter, or one that ends
// undecided, the review is kept pending.
func TestHold_NobodyThereIsPending(t *testing.T) {
	for name, p := range map[string]ReviewPrompter{"no prompter": nil, "undecided": &prompter{err: errors.New("closed")}} {
		t.Run(name, func(t *testing.T) {
			rc, _ := reviewContext(t, p)
			if r, err := rc.Hold(context.Background(), review(1)); err != nil || r.Status != sdk.ReviewPending {
				t.Errorf("%+v %v", r, err)
			}
		})
	}
	bare := NewRunbookContext(nil).(*runbookContext)
	if _, err := bare.Hold(context.Background(), review(1)); !errors.Is(err, sdk.ErrNoAIProject) {
		t.Errorf("no project: %v", err)
	}
}

// TestPause_IsDeclaredNotBuilt: a held error is read as a hold through
// any wrapping, anything else is not, and Resume refuses.
func TestPause_IsDeclaredNotBuilt(t *testing.T) {
	err := methodError("ai.chat", "execute", &sdk.HeldError{Key: "k", Reason: "why"})
	if h, ok := HoldOf(err); !ok || h.Key != "k" || !errors.Is(err, sdk.ErrHeld) {
		t.Errorf("hold %+v %v", h, ok)
	}
	if _, ok := HoldOf(errors.New("plain")); ok {
		t.Error("a plain error is a hold")
	}
	if err := (&Executor{}).Resume(context.Background(), "run"); !errors.Is(err, ErrPauseNotBuilt) || !strings.Contains(err.Error(), "Phase 27") {
		t.Errorf("resume: %v", err)
	}
}
