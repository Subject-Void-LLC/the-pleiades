// Tests for ai.chat and ai.route against a real HTTP server speaking
// Ollama's API, with real ai_provider devices.
package ai_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/ai"
	devai "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

func user(text string) []any { return []any{map[string]any{"role": "user", "content": text}} }

// TestChat_AnswersAndMeters: a call returns the answer and a usage record
// priced by the record's table, appends that record to the sink, and the
// record holds nothing that was sent or answered.
func TestChat_AnswersAndMeters(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = "the secret plan"
	dev := aitest.Local(t, "local", host, false)
	rc := aitest.NewContext()
	res, err := ai.Chat(context.Background(), rc, dev, map[string]any{"model": "m:latest", "messages": user("what is the plan?"), "usage_sink": "spend.jsonl", "residency": "local"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || rc.Stats["answer"] != "the secret plan" || rc.Stats["stop_reason"] != "stop" {
		t.Fatalf("result %+v stats %v", res, rc.Stats)
	}
	lines := rc.UsageLines(t, "ai/usage/spend.jsonl")
	if len(lines) != 1 || lines[0]["cost"] != (10*1.0+4*2.0)/1e6 || lines[0]["currency"] != "USD" || lines[0]["outcome"] != "ok" {
		t.Fatalf("usage %v", lines)
	}
	written := string(rc.Written["ai/usage/spend.jsonl"])
	if strings.Contains(written, "secret plan") || strings.Contains(written, "what is the plan") {
		t.Errorf("the sink holds the conversation: %s", written)
	}
}

// TestChat_CheckCallsNothing: a check validates, estimates and writes
// nothing, and the host sees no chat.
func TestChat_CheckCallsNothing(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	dev := aitest.Local(t, "local", host, false)
	rc := aitest.NewContext()
	desc, _ := collection.Lookup("ai.chat")
	res, err := desc.Check(context.Background(), rc, dev, map[string]any{"model": "m:latest", "messages": user(strings.Repeat("a", 40)), "max_tokens": 10, "usage_sink": "spend.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	est, _ := rc.Stats["estimate"].(map[string]any)
	if !res.Changed || est["input_tokens"] != 10 || est["max_cost"] != (10*1.0+10*2.0)/1e6 {
		t.Errorf("estimate %v", est)
	}
	if host.Chats() != 0 || len(rc.Written) != 0 {
		t.Errorf("a check made %d chats and wrote %v", host.Chats(), rc.Written)
	}
}

// TestChat_RefusesBeforeContact: what a use does not admit is refused with
// the host never reached.
func TestChat_RefusesBeforeContact(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	// openai takes no key (a local server may need none), so a hosted one
	// reaches the residency rule; anthropic always needs one.
	hosted := aitest.Provider(t, "cloud", map[string]any{devai.KindProperty: "openai", devai.APIBaseProperty: "https://api.example.test/v1", devai.ResidencyProperty: "hosted"})
	keyed := aitest.Provider(t, "keyed", map[string]any{devai.KindProperty: "anthropic", devai.APIBaseProperty: "https://api.example.test", devai.ResidencyProperty: "hosted"})
	local := aitest.Local(t, "local", host, false)
	for _, tc := range []struct {
		name   string
		dev    inventory.InventoryItem
		params map[string]any
		want   string
	}{
		{"local use, hosted provider", hosted, map[string]any{"model": "x", "messages": user("hi"), "residency": "local"}, "leave hardware you run"},
		{"a key the kind needs", keyed, map[string]any{"model": "x", "messages": user("hi")}, "add-credential keyed"},
		{"required signature on messages", local, map[string]any{"model": "m", "messages": user("hi"), "signing": "required"}, "not a signed bundle"},
		{"no model", local, map[string]any{"messages": user("hi")}, "model is required"},
		{"a sink outside ai/usage", local, map[string]any{"model": "m", "messages": user("hi"), "usage_sink": "../x.jsonl"}, "usage_sink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ai.Chat(context.Background(), aitest.NewContext(), tc.dev, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
	if host.Chats() != 0 {
		t.Errorf("the host saw %d chats", host.Chats())
	}
}

// TestRoute_FallsBackOnlyWhenTheNextCanMend: an overloaded first provider
// falls back to the second; a first provider refusing the request fails
// the task without asking the second.
func TestRoute_FallsBackOnlyWhenTheNextCanMend(t *testing.T) {
	first, second := aitest.NewOllama(t, "m:latest"), aitest.NewOllama(t, "m:latest")
	second.Answer = "from the second"
	rc := aitest.NewContext()
	rc.Providers = map[string]inventory.InventoryItem{"a": aitest.Local(t, "a", first, false), "b": aitest.Local(t, "b", second, false)}
	params := map[string]any{
		"use": "triage", "messages": user("hi"), "retries": 0, "usage_sink": "spend.jsonl",
		"routes": map[string]any{"triage": map[string]any{"residency": "local", "providers": []any{
			map[string]any{"provider": "a", "model": "m:latest"}, map[string]any{"provider": "b", "model": "m:latest"},
		}}},
	}
	first.FailStatus = http.StatusServiceUnavailable
	if _, err := ai.Route(context.Background(), rc, nil, params); err != nil {
		t.Fatal(err)
	}
	if rc.Stats["provider"] != "b" || rc.Stats["answer"] != "from the second" {
		t.Errorf("stats %v", rc.Stats)
	}
	if lines := rc.UsageLines(t, "ai/usage/spend.jsonl"); len(lines) != 2 || lines[0]["outcome"] != "failed" || lines[1]["outcome"] != "ok" {
		t.Errorf("usage %v", lines)
	}

	first.FailStatus = http.StatusUnauthorized
	before := second.Chats()
	if _, err := ai.Route(context.Background(), aitestContext(rc), nil, params); err == nil || !strings.Contains(err.Error(), "would not mend") {
		t.Errorf("a refused key: %v", err)
	}
	if second.Chats() != before {
		t.Error("a refused request fell back to the next provider")
	}
}

// aitestContext is a fresh context with rc's providers.
func aitestContext(rc *aitest.Context) *aitest.Context {
	c := aitest.NewContext()
	c.Providers = rc.Providers
	return c
}

// TestRoute_ResidencyRefusesTheWholeRoute: a local use naming a hosted
// provider anywhere in its order is refused before the first call, and
// the use, not the task, decides model, signing and residency.
func TestRoute_ResidencyRefusesTheWholeRoute(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	rc := aitest.NewContext()
	rc.Providers = map[string]inventory.InventoryItem{
		"a":     aitest.Local(t, "a", host, false),
		"cloud": aitest.Provider(t, "cloud", map[string]any{devai.KindProperty: "openai", devai.APIBaseProperty: "https://api.example.test/v1", devai.ResidencyProperty: "hosted"}),
	}
	routes := map[string]any{"triage": map[string]any{"residency": "local", "providers": []any{
		map[string]any{"provider": "a", "model": "m:latest"}, map[string]any{"provider": "cloud", "model": "x"},
	}}}
	if _, err := ai.Route(context.Background(), rc, nil, map[string]any{"use": "triage", "routes": routes, "messages": user("hi")}); err == nil || !strings.Contains(err.Error(), "leave hardware you run") {
		t.Errorf("err %v", err)
	}
	for _, k := range []string{"model", "signing", "residency"} {
		params := map[string]any{"use": "triage", "routes": routes, "messages": user("hi"), k: "x"}
		if _, err := ai.Route(context.Background(), rc, nil, params); err == nil || !strings.Contains(err.Error(), "routing table's use") {
			t.Errorf("%s on the task: %v", k, err)
		}
	}
	if _, err := ai.Route(context.Background(), rc, nil, map[string]any{"use": "other", "routes": routes, "messages": user("hi")}); err == nil || !strings.Contains(err.Error(), "triage") {
		t.Errorf("an unknown use: %v", err)
	}
	if host.Chats() != 0 {
		t.Errorf("the host saw %d chats", host.Chats())
	}
}
