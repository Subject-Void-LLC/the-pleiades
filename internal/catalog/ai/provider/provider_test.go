// Tests for ai.provider.list and ai.provider.probe against a real server.
package provider_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/ai/provider"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestList: the record's facts and the live model listing, no chat.
func TestList(t *testing.T) {
	host := aitest.NewOllama(t, "b:latest", "a:latest")
	rc := aitest.NewContext()
	if _, err := provider.List(context.Background(), rc, aitest.Local(t, "ws", host, true), nil); err != nil {
		t.Fatal(err)
	}
	models, _ := rc.Stats["models"].([]any)
	if rc.Stats["kind"] != "ollama" || rc.Stats["residency"] != "local" || rc.Stats["host_guard"] != "single_resident" || len(models) != 2 || models[0] != "a:latest" {
		t.Errorf("stats %v", rc.Stats)
	}
	if host.Chats() != 0 {
		t.Error("listing made a chat")
	}
}

// TestProbe: one capped call, counted; a check makes none.
func TestProbe(t *testing.T) {
	host := aitest.NewOllama(t, "m:latest")
	dev := aitest.Local(t, "ws", host, false)
	desc, _ := collection.Lookup("ai.provider.probe")
	if _, err := desc.Check(context.Background(), aitest.NewContext(), dev, map[string]any{"model": "m:latest"}); err != nil || host.Chats() != 0 {
		t.Fatalf("check: %v, %d chats", err, host.Chats())
	}
	rc := aitest.NewContext()
	if _, err := provider.Probe(context.Background(), rc, dev, map[string]any{"model": "m:latest", "usage_sink": "probe.jsonl"}); err != nil {
		t.Fatal(err)
	}
	usage, _ := rc.Stats["usage"].(map[string]any)
	if host.Chats() != 1 || usage["output_tokens"] != 4 || len(rc.UsageLines(t, "ai/usage/probe.jsonl")) != 1 {
		t.Errorf("chats %d usage %v", host.Chats(), usage)
	}
}
