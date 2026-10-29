// Tests that a rendered parameter declaring a closed set reaches its method
// only as one of its choices, and that the refusal names the set and never
// the value, which may be one a device returned.
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

// runChosen registers t131.chosen, whose state takes present or absent and
// whose method takes GET or HEAD in any case, and runs one task of it with
// params rendered against vars, returning what the method received and the
// task's error.
func runChosen(t *testing.T, params string, vars map[string]any) (map[string]any, error) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{Name: "t131.chosen", Manifest: collection.Manifest{Status: collection.StatusDeclared,
		Doc: collection.Doc{Params: []collection.Param{
			{Name: "state", Type: "string", Choices: []string{"present", "absent"}},
			{Name: "method", Type: "string", Choices: []string{"GET", "HEAD"}, ChoicesIgnoreCase: true},
			{Name: "kinds", Type: "list of string", Choices: []string{"unit", "lint"}},
		}}}}); err != nil {
		t.Fatal(err)
	}
	rec := &paramsRecorder{}
	x := engine.NewExecutor(mapResolver{}, rec, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithVariables(vars), engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, "id: chosen\ntasks:\n  - name: chosen\n    t131.chosen:\n"+params))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rec.got, result.Nodes[0].Err
}

// TestRenderedParams_HeldToTheirChoices covers a rendered value inside the
// set, inside it in another case where the method folds case, and outside
// it as text or as one item of a list.
func TestRenderedParams_HeldToTheirChoices(t *testing.T) {
	vars := map[string]any{"good": "absent", "bad": "s3cret-gone", "lower": "head"}
	for _, tc := range []struct {
		name, params string
		want         []string // substrings of the error; nil means the task runs
	}{
		{"a choice", "      state: \"{{ vars.good }}\"\n", nil},
		{"a folded choice", "      method: \"{{ vars.lower }}\"\n", nil},
		{"outside the set", "      state: \"{{ vars.bad }}\"\n", []string{"parameter state of t131.chosen", "outside its choices", "present, absent"}},
		{"one item outside", "      kinds: [unit, \"{{ vars.bad }}\"]\n", []string{"parameter kinds", "item 1 is not one of unit, lint"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runChosen(t, tc.params, vars)
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
			if strings.Contains(err.Error(), "s3cret") {
				t.Errorf("the error carries the value: %v", err)
			}
			if got != nil {
				t.Errorf("the method ran with %#v", got)
			}
		})
	}
}
