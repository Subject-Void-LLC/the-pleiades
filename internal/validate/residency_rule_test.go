// Tests for ResidencyRule against real ai_provider records.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	devai "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// residencyWorld is one task under hosts: ai, with a local provider, a
// hosted one and a server that is no provider.
func residencyWorld(t *testing.T, fqcn string, params map[string]any) validate.WorldView {
	t.Helper()
	local := aitest.Provider(t, "workstation", map[string]any{devai.KindProperty: "ollama", devai.APIBaseProperty: "http://10.0.0.5:11434", devai.ResidencyProperty: "local"})
	hosted := aitest.Provider(t, "claude", map[string]any{devai.KindProperty: "anthropic", devai.APIBaseProperty: "https://api.anthropic.com", devai.ResidencyProperty: "hosted"})
	server := &inventorytest.Stub{StubName: "web1", StubState: inventory.StateActive}
	return validate.WorldView{
		Items: []inventory.InventoryItem{local, hosted, server},
		DAG: &engine.DAG{
			ID: "t", Hosts: "claude",
			Nodes:     map[string]*engine.Task{"tasks[0]": {Name: "ask", FQCN: fqcn, Params: params}},
			Adjacency: map[string][]engine.EdgeConfig{},
		},
	}
}

func routes(residency string, providers ...string) map[string]any {
	steps := make([]any, len(providers))
	for i, p := range providers {
		steps[i] = map[string]any{"provider": p, "model": "m"}
	}
	return map[string]any{"triage": map[string]any{"residency": residency, "providers": steps}}
}

// TestResidencyRule: each way a local use reaches a hosted provider is
// refused, naming both; what is allowed, and what holds a template, is not.
func TestResidencyRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fqcn   string
		params map[string]any
		want   []string
	}{
		{"chat, local use, hosted target", "ai.chat", map[string]any{"residency": "local"}, []string{"provider claude is hosted"}},
		{"chat, hosted use", "ai.chat", map[string]any{"residency": "hosted"}, nil},
		{"chat, a templated tag", "ai.chat", map[string]any{"residency": "{{ vars.tag }}"}, nil},
		{"route, local use names hosted", "ai.route", map[string]any{"routes": routes("local", "workstation", "claude")}, []string{"use triage", "provider claude is hosted"}},
		{"route, hosted use", "ai.route", map[string]any{"routes": routes("hosted", "claude", "workstation")}, nil},
		{"route, an unknown provider", "ai.route", map[string]any{"routes": routes("local", "nowhere")}, []string{"not in the inventory"}},
		{"route, not a provider", "ai.route", map[string]any{"routes": routes("local", "web1")}, []string{"not an ai_provider"}},
		{"route, a malformed table", "ai.route", map[string]any{"routes": map[string]any{"triage": map[string]any{"providers": []any{}}}}, []string{"residency"}},
		{"route, a templated table", "ai.route", map[string]any{"routes": map[string]any{"triage": "{{ vars.routes }}"}}, nil},
		{"another namespace", "http.request", map[string]any{"residency": "local"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.ResidencyRule(residencyWorld(t, tc.fqcn, tc.params))
			if len(tc.want) == 0 {
				if len(findings) != 0 {
					t.Fatalf("findings %v", findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("findings %v, want one", findings)
			}
			for _, w := range tc.want {
				if !strings.Contains(findings[0].Message, w) {
					t.Errorf("%q does not say %q", findings[0].Message, w)
				}
			}
		})
	}
}
