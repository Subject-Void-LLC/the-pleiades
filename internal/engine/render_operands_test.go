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
