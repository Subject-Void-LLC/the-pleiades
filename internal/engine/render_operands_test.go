// Tests that a rendered parameter declared an operand reaches its method
// only as one name its program cannot read as an option or a pattern, and
// that the refusal never carries the value, which may be one a device
// returned (FAILURE_PATTERNS 1174).
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// runOperand registers t1174.named, whose name and deps are operands, and
// runs one task of it with params rendered against vars, returning what the
// method received and the task's error.
func runOperand(t *testing.T, params string, vars map[string]any) (map[string]any, error) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{Name: "t1174.named", Manifest: collection.Manifest{Status: collection.StatusDeclared,
		Doc: collection.Doc{Params: []collection.Param{
			{Name: "name", Type: "string", Format: collection.ParamFormatOperand},
			{Name: "deps", Type: "list of string", Format: collection.ParamFormatOperand},
			{Name: "note", Type: "string"},
		}}}}); err != nil {
		t.Fatal(err)
	}
	rec := &paramsRecorder{}
	x := engine.NewExecutor(mapResolver{}, rec, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithVariables(vars), engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, "id: named\ntasks:\n  - name: named\n    t1174.named:\n"+params))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rec.got, result.Nodes[0].Err
}

// TestRenderedParams_HeldToTheOperandRule covers a rendered name, a
// rendered option, a rendered pattern, one list item rendered as an option,
// and a parameter that is not an operand, which takes the same text.
func TestRenderedParams_HeldToTheOperandRule(t *testing.T) {
	vars := map[string]any{"good": "openssh-server", "option": "-oDPkg::Pre-Invoke::=touch /tmp/pwned", "pattern": "s3cret*"}
	for _, tc := range []struct {
		name, params string
		want         []string // substrings of the error; nil means the task runs
	}{
		{"a name", "      name: \"{{ vars.good }}\"\n", nil},
		{"an option", "      name: \"{{ vars.option }}\"\n", []string{"parameter name of t1174.named", "cannot pass on as one name", `begins with "-"`}},
		{"a pattern", "      name: \"{{ vars.pattern }}\"\n", []string{"parameter name", "pattern"}},
		{"one item an option", "      deps: [libjq1, \"{{ vars.option }}\"]\n", []string{"parameter deps", `item 1 is refused: it begins with "-"`}},
		{"not an operand", "      note: \"{{ vars.option }}\"\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runOperand(t, tc.params, vars)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("the task failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the task ran with %#v", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to say %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "Pre-Invoke") {
				t.Errorf("the error carries the value: %v", err)
			}
			if got != nil {
				t.Errorf("the method ran with %#v", got)
			}
		})
	}
}

// TestRenderedParams_HeldToEachProgramsGrammar: every operand format, not
// only the general one, holds a rendered value to its own rule (each a
// pkg/operand kind, through collection.OperandProblem). A register or loop
// item that renders to systemd's root mount reaches a unit parameter, one
// that renders to "diffutils-" never reaches an APT name, and a rendered
// version or mount field is held to its rule too.
func TestRenderedParams_HeldToEachProgramsGrammar(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{Name: "t1174.grammar", Manifest: collection.Manifest{Status: collection.StatusDeclared,
		Doc: collection.Doc{Params: []collection.Param{
			{Name: "unit", Type: "string", Format: collection.ParamFormatSystemdUnit},
			{Name: "apt", Type: "string", Format: collection.ParamFormatAptPackage},
			{Name: "rpm", Type: "string", Format: collection.ParamFormatRPMPackage},
			{Name: "version", Type: "string", Format: collection.ParamFormatPackageVersion},
			{Name: "path", Type: "string", Format: collection.ParamFormatMountField},
		}}}}); err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"v": ""}
	for _, tc := range []struct {
		param, value string
		want         string // a substring of the error; "" means the task runs
	}{
		{"unit", "-.mount", ""},
		{"unit", "-.slice", ""},
		{"unit", "-f", `begins with "-"`},
		{"apt", "diffutils-", `ends in "-"`},
		{"apt", "jq/bookworm", `holds "/"`},
		{"rpm", "@core", `begins with "@"`},
		{"version", "*", "pattern"},
		{"version", "1.0-", `ends in "-"`},
		{"path", "-a", `begins with "-"`},
		{"path", "/mnt/a b", "space"},
		{"path", "/srv/[x]", ""},
	} {
		t.Run(tc.param+" "+tc.value, func(t *testing.T) {
			vars["v"] = tc.value
			rec := &paramsRecorder{}
			x := engine.NewExecutor(mapResolver{}, rec, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
				engine.WithVariables(vars), engine.WithRenderer(render.New()))
			result, err := x.Run(context.Background(), yamlRunbook(t, "id: grammar\ntasks:\n  - name: grammar\n    t1174.grammar:\n      "+tc.param+": \"{{ vars.v }}\"\n"))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := result.Nodes[0].Err
			if tc.want == "" {
				if got != nil {
					t.Fatalf("the task failed: %v", got)
				}
				if rec.got[tc.param] != tc.value {
					t.Errorf("the method received %#v, want %s = %q", rec.got, tc.param, tc.value)
				}
				return
			}
			if got == nil || !strings.Contains(got.Error(), tc.want) || !strings.Contains(got.Error(), "cannot pass on as one name") {
				t.Fatalf("err = %v, want it refused saying %q", got, tc.want)
			}
		})
	}
}
