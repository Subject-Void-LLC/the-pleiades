// Package aitask: the review gate a call passes before and after it is
// made, through the run's sdk.Reviewer.
package aitask

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Gate is one request's review: its rules, its key, and the run's
// reviewer, nil when the run has none (the Walk tier until Phase 27).
type Gate struct {
	Rules    []Rule
	Key      string
	reviewer sdk.Reviewer
}

// NewGate is the review of the request keyed key under rules.
func NewGate(rc sdk.RunbookContext, rules []Rule, key string) Gate {
	g := Gate{Rules: rules, Key: key}
	g.reviewer, _ = rc.(sdk.Reviewer)
	return g
}

// On reports whether the request has rules to answer to.
func (g Gate) On() bool { return len(g.Rules) > 0 }

// decideCommand is how a person decides a pending review.
func decideCommand(key string) string {
	return fmt.Sprintf("decide it with `pleiades ai review show %s`, then `approve` or `deny`, and run again", key)
}

// Before looks the request up before any call, so asking again never
// asks or bills twice: an approved review is returned for its answer to
// be reused; a denied one fails; a pending one fails as still held. With
// no review on record, or no rules, it returns nothing and the call goes
// ahead.
func (g Gate) Before() (*sdk.Review, error) {
	if !g.On() || g.reviewer == nil {
		return nil, nil
	}
	r, found, err := g.reviewer.Decided(g.Key)
	switch {
	case err != nil:
		// A run with no project has no reviews to find; After fails it
		// closed if its answer is held.
		if errors.Is(err, sdk.ErrNoAIProject) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading review %s: %w", g.Key, err)
	case !found:
		return nil, nil
	case r.Status == sdk.ReviewApproved:
		return &r, nil
	case r.Status == sdk.ReviewDenied:
		return nil, fmt.Errorf("this request's answer was denied by %s at %s (review %s)%s", r.DecidedBy, r.DecidedAt, g.Key, comment(r))
	}
	return nil, &sdk.HeldError{Key: g.Key, Reason: fmt.Sprintf("this request's answer has been held since %s; %s", r.HeldAt, decideCommand(g.Key))}
}

// After holds an answer whose outcome matches a rule. It returns the
// review when a person approved it, nil when no rule matched, and an
// error when the answer is not released: denied, pending, or with nobody
// to ask (which fails closed, so a run without a reviewer never releases
// what it would have held).
func (g Gate) After(ctx context.Context, r sdk.Review, o Outcome) (*sdk.Review, error) {
	reasons := Reasons(g.Rules, o)
	if len(reasons) == 0 {
		return nil, nil
	}
	r.Key, r.Reasons = g.Key, reasons
	if g.reviewer == nil {
		return nil, &sdk.HeldError{Key: g.Key, Reason: noReviewer(reasons)}
	}
	held, err := g.reviewer.Hold(ctx, r)
	if errors.Is(err, sdk.ErrNoAIProject) {
		return nil, &sdk.HeldError{Key: g.Key, Reason: noReviewer(reasons)}
	}
	if err != nil {
		return nil, &sdk.HeldError{Key: g.Key, Reason: fmt.Sprintf("the answer matched %v and could not be held for review: %v; it is not released", reasons, err)}
	}
	// A review kept under this key holds the answer it was made for; an
	// approval of that one is no approval of another that arrived while it
	// was being decided (two calls with one request key in flight).
	if held.Answer != r.Answer {
		return nil, &sdk.HeldError{Key: g.Key, Reason: fmt.Sprintf("the answer matched %v, and a review under this request's key already holds a different answer; this one is not released, and an approval is of the answer a person was shown", reasons)}
	}
	switch held.Status {
	case sdk.ReviewApproved:
		return &held, nil
	case sdk.ReviewDenied:
		return nil, fmt.Errorf("the answer was denied by %s (review %s)%s", held.DecidedBy, g.Key, comment(held))
	}
	return nil, &sdk.HeldError{Key: g.Key, Reason: fmt.Sprintf("the answer matched %v; %s", reasons, decideCommand(g.Key))}
}

// noReviewer is why an answer held with nobody to ask is not released.
func noReviewer(reasons []string) string {
	return fmt.Sprintf("the answer matched %v, and this run cannot hold it for a person (the Walk tier cannot until Phase 27); it is not released", reasons)
}

// comment is a review's comment, as an error's tail.
func comment(r sdk.Review) string {
	if r.Comment == "" {
		return ""
	}
	return ": " + r.Comment
}

// Peek is what a check reports of the review: the rules, and a decision
// already on record for the request, read without changing anything.
func (g Gate) Peek() map[string]any {
	if !g.On() {
		return nil
	}
	rules := make([]any, len(g.Rules))
	for i, r := range g.Rules {
		rules[i] = r.String()
	}
	out := map[string]any{"key": g.Key, "rules": rules, "status": "a run holds the answer when a rule matches"}
	if g.reviewer == nil {
		out["status"] = "this run cannot hold an answer for a person, so one matching a rule fails closed"
		return out
	}
	if r, found, err := g.reviewer.Decided(g.Key); err == nil && found {
		out["status"], out["decided_by"] = string(r.Status), r.DecidedBy
	}
	return out
}

// Stat is a released answer's review as a return value: approved by whom,
// or not held when no rule matched.
func Stat(g Gate, r *sdk.Review) map[string]any {
	if !g.On() {
		return nil
	}
	if r == nil {
		return map[string]any{"status": "not held", "key": g.Key}
	}
	return map[string]any{"status": string(r.Status), "key": g.Key, "reasons": list(r.Reasons), "decided_by": r.DecidedBy, "decided_at": r.DecidedAt}
}
