// Tests for ai.bench.run: scorers, a set run against a real server, and
// its saved results.
package bench_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/ai/bench"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func prompt(id, text string, expect map[string]any) map[string]any {
	return map[string]any{"id": id, "text": text, "expect": expect}
}

// TestRun_ScoresEachPrompt: each scorer judges the same answer, the score
// is passed over count, the cost is every call's, and save_as keeps the
// results with their answers.
func TestRun_ScoresEachPrompt(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = `{"label":"bug","meta":{"confidence":0.9}}`
	dev := aitest.Local(t, "ws", host, false)
	rc := aitest.NewContext()
	set := []any{
		prompt("exact", "x", map[string]any{"exact": `{"label":"bug","meta":{"confidence":0.9}}`}),
		prompt("contains", "x", map[string]any{"contains": "bug"}),
		prompt("regex", "x", map[string]any{"regex": `"label":"(bug|outage)"`}),
		prompt("field", "x", map[string]any{"json_field": map[string]any{"path": "meta.confidence", "equals": 0.9}}),
		prompt("miss", "x", map[string]any{"json_field": map[string]any{"path": "label", "equals": "outage"}}),
	}
	if _, err := bench.Run(context.Background(), rc, dev, map[string]any{"model": "m:latest", "prompts": set, "save_as": "run1.json", "usage_sink": "bench.jsonl"}); err != nil {
		t.Fatal(err)
	}
	if rc.Stats["passed"] != 4 || rc.Stats["count"] != 5 || rc.Stats["score"] != 0.8 || rc.Stats["cost"] != 5*(10*1.0+4*2.0)/1e6 {
		t.Errorf("stats %v", rc.Stats)
	}
	var saved map[string]any
	if err := json.Unmarshal(rc.Written["ai/bench/run1.json"], &saved); err != nil {
		t.Fatal(err)
	}
	if results, _ := saved["results"].([]any); len(results) != 5 || results[0].(map[string]any)["answer"] != host.Answer {
		t.Errorf("saved %v", saved)
	}
	if len(rc.UsageLines(t, "ai/usage/bench.jsonl")) != 5 {
		t.Error("not every call's usage was appended")
	}
	calls := host.Chats()
	if _, err := bench.Run(context.Background(), rc, dev, map[string]any{"model": "m:latest", "prompts": set, "save_as": "run1.json"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a result written over: %v", err)
	}
	if host.Chats() != calls {
		t.Errorf("a save_as already taken was found after %d billed calls, not before any", host.Chats()-calls)
	}
}

// TestRun_RefusesABadSet before any call, and a check calls nothing.
func TestRun_RefusesABadSet(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	dev := aitest.Local(t, "ws", host, false)
	for _, set := range []any{
		[]any{}, []any{prompt("a", "x", nil)}, []any{prompt("a", "x", map[string]any{"regex": "("})},
		[]any{prompt("a", "x", map[string]any{"exact": "y", "contains": "y"})}, []any{prompt("a", "x", map[string]any{"json_field": map[string]any{"path": "p"}})},
		[]any{prompt("a", "x", map[string]any{"exact": "y"}), prompt("a", "x", map[string]any{"exact": "y"})},
		[]any{map[string]any{"id": "a", "text": "x", "messages": []any{}, "expect": map[string]any{"exact": "y"}}},
		[]any{prompt("bad id", "x", map[string]any{"exact": "y"})},
	} {
		if _, err := bench.Run(context.Background(), aitest.NewContext(), dev, map[string]any{"model": "m", "prompts": set}); err == nil {
			t.Errorf("set %v was accepted", set)
		}
	}
	desc, _ := collection.Lookup("ai.bench.run")
	rc := aitest.NewContext()
	if _, err := desc.Check(context.Background(), rc, dev, map[string]any{"model": "m:latest", "prompts": []any{prompt("a", strings.Repeat("x", 8), map[string]any{"exact": "y"})}}); err != nil {
		t.Fatal(err)
	}
	if host.Chats() != 0 || rc.Stats["count"] != 1 {
		t.Errorf("chats %d stats %v", host.Chats(), rc.Stats)
	}
}
