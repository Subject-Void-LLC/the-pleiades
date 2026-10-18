// Tests for human review in ai.chat and ai.route: an answer crossing a
// threshold is held, never read by a later task until a person decides,
// and asking again reuses the decision with no second call.
package ai_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/ai"
	devai "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// lowConfidence is a task whose answer holds when its confidence is
// under 0.8.
func lowConfidence() map[string]any {
	return map[string]any{
		"model": "m:latest", "messages": user("classify"),
		"review": []any{map[string]any{"field": "confidence", "below": 0.8}},
	}
}

// TestChat_HeldUntilDecided: a held answer fails the task as held and is
// in no stat; asking again while pending makes no call; once approved,
// asking again returns the approved answer with no call.
func TestChat_HeldUntilDecided(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = `{"label":"bug","confidence":0.5}`
	dev := aitest.Local(t, "local", host, false)
	rc := aitest.NewContext()

	_, err := ai.Chat(context.Background(), rc, dev, lowConfidence())
	var held *sdk.HeldError
	if !errors.As(err, &held) || !errors.Is(err, sdk.ErrHeld) || !strings.Contains(err.Error(), "pleiades ai review show") {
		t.Fatalf("err %v, want a hold naming the review command", err)
	}
	if _, set := rc.Stats["answer"]; set {
		t.Fatal("a held answer was returned")
	}
	r := rc.Reviews[held.Key]
	if r.Status != sdk.ReviewPending || r.Answer != host.Answer || len(r.Reasons) != 1 || !strings.Contains(r.Reasons[0], "it is 0.5") {
		t.Fatalf("review %+v", r)
	}

	if _, err := ai.Chat(context.Background(), aitestWith(rc), dev, lowConfidence()); !errors.Is(err, sdk.ErrHeld) || host.Chats() != 1 {
		t.Fatalf("pending: %v, %d chats", err, host.Chats())
	}

	r.Status, r.DecidedBy = sdk.ReviewApproved, "ops"
	rc.Reviews[held.Key] = r
	again := aitestWith(rc)
	if _, err := ai.Chat(context.Background(), again, dev, lowConfidence()); err != nil {
		t.Fatal(err)
	}
	if host.Chats() != 1 || again.Stats["answer"] != host.Answer || again.Stats["reused"] != true {
		t.Errorf("approved: %d chats, stats %v", host.Chats(), again.Stats)
	}
	if rv, _ := again.Stats["review"].(map[string]any); rv["status"] != "approved" || rv["decided_by"] != "ops" {
		t.Errorf("review stat %v", again.Stats["review"])
	}

	// A different request is a different review.
	other := lowConfidence()
	other["messages"] = user("classify this other one")
	if _, err := ai.Chat(context.Background(), aitestWith(rc), dev, other); !errors.Is(err, sdk.ErrHeld) || host.Chats() != 2 {
		t.Errorf("another request: %v, %d chats", err, host.Chats())
	}
}

// aitestWith is a fresh context sharing rc's reviews.
func aitestWith(rc *aitest.Context) *aitest.Context {
	c := aitest.NewContext()
	c.Reviews, c.Providers = rc.Reviews, rc.Providers
	return c
}

// TestChat_DecidedAtTheTerminal: a person approving at once releases the
// answer in the same run; denying fails the task as failed, not held.
func TestChat_DecidedAtTheTerminal(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = `{"confidence":0.1}`
	dev := aitest.Local(t, "local", host, false)
	rc := aitest.NewContext()
	rc.Decide = func(sdk.Review) sdk.ReviewStatus { return sdk.ReviewApproved }
	if _, err := ai.Chat(context.Background(), rc, dev, lowConfidence()); err != nil || rc.Stats["answer"] != host.Answer {
		t.Fatalf("approved: %v %v", err, rc.Stats)
	}
	denied := aitest.NewContext()
	denied.Decide = func(sdk.Review) sdk.ReviewStatus { return sdk.ReviewDenied }
	if _, err := ai.Chat(context.Background(), denied, dev, lowConfidence()); err == nil || errors.Is(err, sdk.ErrHeld) || !strings.Contains(err.Error(), "denied by tester") {
		t.Errorf("denied: %v", err)
	}
}

// TestChat_NobodyToAskFailsClosed: with no reviewer (a Walk Runner) an
// answer crossing a threshold is never released.
func TestChat_NobodyToAskFailsClosed(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = `{"confidence":0.1}`
	rc := aitest.NewContext()
	_, err := ai.Chat(context.Background(), aitest.Unreviewed{C: rc}, aitest.Local(t, "local", host, false), lowConfidence())
	if !errors.Is(err, sdk.ErrHeld) || !strings.Contains(err.Error(), "Phase 27") {
		t.Fatalf("err %v", err)
	}
	if _, set := rc.Stats["answer"]; set {
		t.Error("an answer was released with nobody to ask")
	}
}

// TestChat_RulesThatCannotBeEvaluatedHold: an unpriced call under
// cost_over and an answer that is not JSON under a field rule both hold;
// an answer clear of every rule is released, marked not held.
func TestChat_RulesThatCannotBeEvaluatedHold(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = "plain text"
	unpriced := aitest.Provider(t, "free", map[string]any{devai.KindProperty: "ollama", devai.APIBaseProperty: host.URL, devai.ResidencyProperty: "local"})
	for name, rule := range map[string]map[string]any{
		"unpriced": {"cost_over": 1}, "not JSON": {"field": "x", "equals": 1},
	} {
		params := map[string]any{"model": "m:latest", "messages": user("hi"), "review": []any{rule}}
		if _, err := ai.Chat(context.Background(), aitest.NewContext(), unpriced, params); !errors.Is(err, sdk.ErrHeld) {
			t.Errorf("%s: %v", name, err)
		}
	}
	rc := aitest.NewContext()
	clear := map[string]any{"model": "m:latest", "messages": user("hi"), "review": []any{map[string]any{"answer_matches": "(?i)drop table"}, map[string]any{"output_tokens_over": 100}}}
	if _, err := ai.Chat(context.Background(), rc, aitest.Local(t, "local", host, false), clear); err != nil {
		t.Fatal(err)
	}
	if rv, _ := rc.Stats["review"].(map[string]any); rv["status"] != "not held" {
		t.Errorf("review stat %v", rc.Stats["review"])
	}
}

// TestRoute_UseHoldsAFallback: a use whose review holds an answer from
// a fallback provider holds the second provider's answer, and a check
// reports the use's rules with no call.
func TestRoute_UseHoldsAFallback(t *testing.T) {
	first, second := aitest.NewOllama(t, "m:latest"), aitest.NewOllama(t, "m:latest")
	first.FailStatus = http.StatusServiceUnavailable
	rc := aitest.NewContext()
	rc.Providers = map[string]inventory.InventoryItem{"a": aitest.Local(t, "a", first, false), "b": aitest.Local(t, "b", second, false)}
	params := map[string]any{
		"use": "triage", "messages": user("hi"), "retries": 0,
		"routes": map[string]any{"triage": map[string]any{"residency": "local", "review": []any{map[string]any{"fallback": true}}, "providers": []any{
			map[string]any{"provider": "a", "model": "m:latest"}, map[string]any{"provider": "b", "model": "m:latest"},
		}}},
	}
	desc, _ := collection.Lookup("ai.route")
	check := aitestWith(rc)
	if _, err := desc.Check(context.Background(), check, nil, params); err != nil {
		t.Fatal(err)
	}
	if rv, _ := check.Stats["review"].(map[string]any); rv == nil || len(rv["rules"].([]any)) != 1 || first.Chats()+second.Chats() != 0 {
		t.Fatalf("check: %v, %d chats", check.Stats["review"], first.Chats()+second.Chats())
	}
	if _, err := ai.Route(context.Background(), rc, nil, params); !errors.Is(err, sdk.ErrHeld) || second.Chats() != 1 {
		t.Fatalf("err %v, %d chats on the second", err, second.Chats())
	}
	for _, r := range rc.Reviews {
		if r.Provider != "b" || !strings.Contains(strings.Join(r.Reasons, " "), "fallback") {
			t.Errorf("review %+v", r)
		}
	}
}
