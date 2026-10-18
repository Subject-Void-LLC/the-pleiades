// Tests for review rules: what each criterion matches, what is refused,
// and what the request key covers.
package aitask

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func rules(t *testing.T, raw ...map[string]any) []Rule {
	t.Helper()
	list := make([]any, len(raw))
	for i, r := range raw {
		list[i] = r
	}
	out, err := ParseReview(list)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRule_Match: each criterion, matching and not.
func TestRule_Match(t *testing.T) {
	json := `{"label":"bug","meta":{"confidence":0.75,"tags":["a"]}}`
	for _, tc := range []struct {
		rule map[string]any
		o    Outcome
		hold bool
	}{
		{map[string]any{"cost_over": 0.5}, Outcome{Cost: 0.6, Priced: true}, true},
		{map[string]any{"cost_over": 0.5}, Outcome{Cost: 0.4, Priced: true}, false},
		{map[string]any{"cost_over": 0.5}, Outcome{}, true},
		{map[string]any{"output_tokens_over": 100}, Outcome{OutputTokens: 101, Reported: true}, true},
		{map[string]any{"output_tokens_over": 100}, Outcome{OutputTokens: 100, Reported: true}, false},
		{map[string]any{"output_tokens_over": 100}, Outcome{}, true},
		{map[string]any{"answer_matches": "(?i)drop\\s+table"}, Outcome{Answer: "then DROP  TABLE users"}, true},
		{map[string]any{"answer_matches": "(?i)drop\\s+table"}, Outcome{Answer: "fine"}, false},
		{map[string]any{"stop_reason": []any{"max_tokens", "length"}}, Outcome{StopReason: "length"}, true},
		{map[string]any{"stop_reason": []any{"max_tokens"}}, Outcome{StopReason: "stop"}, false},
		{map[string]any{"fallback": true}, Outcome{Fallback: true}, true},
		{map[string]any{"fallback": true}, Outcome{}, false},
		{map[string]any{"unsigned": true}, Outcome{}, true},
		{map[string]any{"unsigned": true}, Outcome{Verified: true}, false},
		{map[string]any{"always": true}, Outcome{Verified: true}, true},
		{map[string]any{"field": "meta.confidence", "below": 0.8}, Outcome{Answer: json}, true},
		{map[string]any{"field": "meta.confidence", "above": 0.8}, Outcome{Answer: json}, false},
		{map[string]any{"field": "meta.confidence", "equals": 0.75}, Outcome{Answer: json}, true},
		{map[string]any{"field": "label", "in": []any{"outage", "bug"}}, Outcome{Answer: json}, true},
		{map[string]any{"field": "label", "not_in": []any{"bug", "chore"}}, Outcome{Answer: json}, false},
		{map[string]any{"field": "label", "not_in": []any{"chore"}}, Outcome{Answer: json}, true},
		{map[string]any{"field": "meta.tags", "equals": []any{"a"}}, Outcome{Answer: json}, true},
		{map[string]any{"field": "missing", "equals": 1}, Outcome{Answer: json}, true},
		{map[string]any{"field": "label", "below": 1}, Outcome{Answer: json}, true},
		{map[string]any{"field": "label", "equals": "bug"}, Outcome{Answer: "not json"}, true},
	} {
		got := rules(t, tc.rule)[0].Match(tc.o)
		if (got != "") != tc.hold {
			t.Errorf("%v on %+v: %q, want hold %v", tc.rule, tc.o, got, tc.hold)
		}
	}
}

// TestParseReview_Refuses: every rule that is not one.
func TestParseReview_Refuses(t *testing.T) {
	for _, bad := range []any{
		[]any{}, "always", []any{"always"}, []any{map[string]any{}},
		[]any{map[string]any{"cost_over": -1}}, []any{map[string]any{"cost_over": "1"}},
		[]any{map[string]any{"output_tokens_over": 1.5}}, []any{map[string]any{"answer_matches": "("}},
		[]any{map[string]any{"answer_matches": strings.Repeat("a", 1025)}},
		[]any{map[string]any{"always": false}}, []any{map[string]any{"fallback": "yes"}},
		[]any{map[string]any{"always": true, "unsigned": true}}, []any{map[string]any{"stop_reason": []any{}}},
		[]any{map[string]any{"field": "x"}}, []any{map[string]any{"field": "x", "below": 1, "above": 2}},
		[]any{map[string]any{"field": "x", "near": 1}}, []any{map[string]any{"field": ".x", "equals": 1}},
		[]any{map[string]any{"field": "x", "below": "low"}}, []any{map[string]any{"field": "x", "in": []any{}}},
		[]any{map[string]any{"approve": true}},
	} {
		if _, err := ParseReview(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	if r, err := ParseReview(nil); r != nil || err != nil {
		t.Errorf("absent: %v %v", r, err)
	}
}

// TestRequestKey: the same request keys the same; what it asks changes
// the key, how hard it tries and where its record goes do not.
func TestRequestKey(t *testing.T) {
	base := map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "temperature": 0.2}
	k1, _ := RequestKey("ai.chat", "p", "", base)
	k2, _ := RequestKey("ai.chat", "p", "", map[string]any{"temperature": 0.2, "model": "m", "messages": []any{map[string]any{"content": "hi", "role": "user"}}, "retries": 9, "timeout": "1m", "usage_sink": "x.jsonl"})
	if k1 != k2 || len(k1) != 64 {
		t.Errorf("%s != %s", k1, k2)
	}
	for name, change := range map[string]func() (string, error){
		"another target": func() (string, error) { return RequestKey("ai.chat", "q", "", base) },
		"another method": func() (string, error) { return RequestKey("ai.route", "p", "", base) },
		"another bundle": func() (string, error) { return RequestKey("ai.chat", "p", "abc", base) },
		"another temperature": func() (string, error) {
			c := clone(base)
			c["temperature"] = 0.3
			return RequestKey("ai.chat", "p", "", c)
		},
		"another message": func() (string, error) {
			c := clone(base)
			c["messages"] = []any{}
			return RequestKey("ai.chat", "p", "", c)
		},
	} {
		if k, _ := change(); k == k1 {
			t.Errorf("%s keyed the same", name)
		}
	}
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// stubReviewer keeps the first review made under a key and returns it for
// every later one, as the project's reviews do.
type stubReviewer struct{ kept sdk.Review }

func (s *stubReviewer) Decided(string) (sdk.Review, bool, error) { return s.kept, true, nil }
func (s *stubReviewer) Hold(_ context.Context, r sdk.Review) (sdk.Review, error) {
	return s.kept, nil
}

// TestGate_AnApprovalIsOfTheAnswerShown: a second answer reaching a key
// whose review a person approved for a first answer is not released under
// that approval.
func TestGate_AnApprovalIsOfTheAnswerShown(t *testing.T) {
	held := rules(t, map[string]any{"always": true})
	first := sdk.Review{Key: "k", Answer: "first", Status: sdk.ReviewApproved}
	g := Gate{Rules: held, Key: "k", reviewer: &stubReviewer{kept: first}}
	if got, err := g.After(context.Background(), sdk.Review{Answer: "first"}, Outcome{}); err != nil || got == nil {
		t.Fatalf("the approved answer itself = %v, %v", got, err)
	}
	got, err := g.After(context.Background(), sdk.Review{Answer: "second, different"}, Outcome{})
	var heldErr *sdk.HeldError
	if got != nil || !errors.As(err, &heldErr) {
		t.Errorf("a different answer under an approval = %v, %v; want it held", got, err)
	}
}
