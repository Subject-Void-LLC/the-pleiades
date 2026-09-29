// Package validate_test: tests of the template rule (Phase 117a).
package validate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerFormatted registers fixture methods whose parameters carry the
// command and URL formats, for the rest of the test.
func registerFormatted(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	for _, d := range []collection.Descriptor{
		{Name: "t117.shell", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "cmd", Type: "string", Format: collection.ParamFormatCommand}}}}},
		{Name: "t117.api", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "url", Type: "string", Format: collection.ParamFormatURL}, {Name: "body", Type: "string"}}}}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTemplateRule: each rendered parameter a runbook author could get
// wrong is refused before the run, naming the task, the parameter and why;
// the safe spellings pass.
func TestTemplateRule(t *testing.T) {
	registerFormatted(t)
	const head = "id: t\ntasks:\n  - name: read the ticket\n    t117.api:\n      url: https://itsm.example.com/api/now/table/incident/INC1\n    register: ticket\n"
	for _, tc := range []struct {
		name string
		task string
		want string
	}{
		{"shell with quote", "t117.shell:\n      cmd: \"echo {{ result.ticket.json.x | quote }}\"", ""},
		{"shell with cli_token", "t117.shell:\n      cmd: \"show interface {{ result.ticket.json.ci | cli_token }}\"", ""},
		{"shell without a safe filter", "t117.shell:\n      cmd: \"echo {{ result.ticket.json.x }}\"", "command text"},
		{"shell ending on another filter", "t117.shell:\n      cmd: \"echo {{ result.ticket.json.x | quote | upper }}\"", "command text"},
		{"url with a fixed host", "t117.api:\n      url: \"https://itsm.example.com/api/{{ vars.id | urlencode }}\"", ""},
		{"url path on the device", "t117.api:\n      url: \"/api/{{ vars.id | urlencode }}\"", ""},
		{"url with a templated host", "t117.api:\n      url: \"https://{{ vars.host }}/api\"", "is a URL"},
		{"url extending the host", "t117.api:\n      url: \"https://itsm.example.com{{ vars.path }}\"", "is a URL"},
		{"url that is all expression", "t117.api:\n      url: \"{{ vars.url }}\"", "is a URL"},
		{"body reading the ticket", "t117.api:\n      url: /api/x\n      body: \"{{ result.ticket.json | to_json }}\"", ""},
		{"an unknown root", "t117.api:\n      url: /api/x\n      body: \"{{ secrets.token }}\"", "may read only"},
		{"a register nobody writes", "t117.api:\n      url: /api/x\n      body: \"{{ nodes.nothing }}\"", "no task in this runbook registers"},
		{"an unknown filter", "t117.api:\n      url: /api/x\n      body: \"{{ vars.x | shell }}\"", "unknown filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlDAG(t, head+"  - name: the task\n    "+tc.task+"\n")
			findings := validate.TemplateRule(validate.WorldView{DAG: dag})
			switch {
			case tc.want == "" && len(findings) > 0:
				t.Fatalf("refused a safe parameter: %v", findings)
			case tc.want != "" && (len(findings) == 0 || !strings.Contains(findings[0].Message, tc.want)):
				t.Fatalf("findings %v, want one saying %q", findings, tc.want)
			}
		})
	}

	self := yamlDAG(t, "id: s\ntasks:\n  - name: reads itself\n    t117.api:\n      url: /api/x\n      body: \"{{ result.me }}\"\n    register: me\n")
	if f := validate.TemplateRule(validate.WorldView{DAG: self}); len(f) != 1 || !strings.Contains(f[0].Message, "own register") {
		t.Errorf("a task reading its own register: findings %v", f)
	}

	// A task with no name is named by its id alone, never by an empty
	// name in quotes.
	unnamed := yamlDAG(t, "id: u\ntasks:\n  - t117.api:\n      url: /api/x\n      body: \"{{ nodes.nothing }}\"\n")
	if f := validate.TemplateRule(validate.WorldView{DAG: unnamed}); len(f) != 1 || !strings.HasPrefix(f[0].Message, "task tasks[0], parameter body:") {
		t.Errorf("an unnamed task: findings %v", f)
	}
}
