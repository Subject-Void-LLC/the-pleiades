// Tests for ParamsRule against the real registered catalog, so the
// declared parameter lists it reads are the ones the binary ships.
package validate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// dagWithParams builds a one-task DAG calling fqcn with params.
func dagWithParams(fqcn string, params map[string]any) *engine.DAG {
	return &engine.DAG{
		ID:        "p",
		Nodes:     map[string]*engine.Task{"tasks[0]": {Name: "step", FQCN: fqcn, Params: params}},
		Adjacency: map[string][]engine.EdgeConfig{},
	}
}

// TestParamsRule covers what the rule refuses and what it lets through:
// a method's own params and its fragments' params pass, the engine's own
// target passes, an undeclared key is refused by name with the accepted
// list, and an engine action or an unknown or declared-only method is
// left to the rules that own it.
func TestParamsRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fqcn   string
		params map[string]any
		want   []string // substrings of the one finding; nil means no finding
	}{
		{"declared params pass", "exec.command", map[string]any{"cmd": "true", "creates": "/x"}, nil},
		{"target is the engine's", "pkg.apt.install", map[string]any{"name": "curl", "target": "web1"}, nil},
		{"fragment params pass", "net.catalyst.device_facts", map[string]any{"insecure_skip_verify": true, "page_size": 100}, nil},
		{
			"misspelled optional param", "exec.command", map[string]any{"cmd": "true", "creats": "/x"},
			[]string{`task tasks[0] (name "step") passes parameter "creats"`, "exec.command does not declare", "accepts argv, chdir, cmd, creates"},
		},
		{
			"params copied from another method", "net.cli.command", map[string]any{"command": "show clock", "prompt": []any{"x"}, "answer": []any{"y"}},
			[]string{`passes parameters "answer", "prompt"`},
		},
		{"engine action", "noop", map[string]any{"anything": 1}, nil},
		{"unregistered method", "totally.fake.name", map[string]any{"x": 1}, nil},
		{"declared-only method", "file.template", map[string]any{"x": 1}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.ParamsRule(validate.WorldView{DAG: dagWithParams(tc.fqcn, tc.params)})
			if tc.want == nil {
				if len(findings) != 0 {
					t.Errorf("findings = %v, want none", findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("findings = %v, want exactly one", findings)
			}
			if findings[0].RuleName != "params" || findings[0].Node != "tasks[0]" {
				t.Errorf("finding = %+v, want rule params on tasks[0]", findings[0])
			}
			for _, w := range tc.want {
				if !strings.Contains(findings[0].Message, w) {
					t.Errorf("message %q does not contain %q", findings[0].Message, w)
				}
			}
		})
	}
}

// TestParamsRule_UndocumentedExternalMethod holds a method that declares
// no params, as an external Collection program may, to what it declares:
// its parameter is refused and the message says it accepts none, so the
// fix (document the parameter) is plain.
func TestParamsRule_UndocumentedExternalMethod(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	noop := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{
		Name:     "extfixture.undocumented",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"}},
		Invoke:   noop,
		Provider: &collection.Provider{Program: "/fixture", Digest: "sha256:00"},
	}); err != nil {
		t.Fatal(err)
	}
	findings := validate.ParamsRule(validate.WorldView{DAG: dagWithParams("extfixture.undocumented", map[string]any{"level": 3})})
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "accepts no parameters") {
		t.Errorf("findings = %v, want one saying the method accepts no parameters", findings)
	}
}

// TestValidate_FindingsAreSorted proves Validate returns the same runbook's
// findings in one stable order, node first, however the rules walked the
// DAG's node map.
func TestValidate_FindingsAreSorted(t *testing.T) {
	dag := &engine.DAG{ID: "s", Adjacency: map[string][]engine.EdgeConfig{}, Nodes: map[string]*engine.Task{}}
	for _, id := range []string{"tasks[3]", "tasks[0]", "tasks[2]", "tasks[1]"} {
		dag.Nodes[id] = &engine.Task{FQCN: "totally.fake.name"}
	}
	first := validate.Validate(validate.WorldView{DAG: dag}).String()
	for range 20 {
		if again := validate.Validate(validate.WorldView{DAG: dag}).String(); again != first {
			t.Fatalf("findings changed order between runs:\n%s\nvs\n%s", first, again)
		}
	}
	if !strings.Contains(first, `"tasks[0]"`) || strings.Index(first, `"tasks[0]"`) > strings.Index(first, `"tasks[3]"`) {
		t.Errorf("findings are not in node order:\n%s", first)
	}
}
