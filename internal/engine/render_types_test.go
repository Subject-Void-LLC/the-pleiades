// Tests that a rendered parameter reaches its method as the type the method
// declares: text for a string, the value itself for anything else, and a
// refusal naming the parameter when the rendered value is of another type.
package engine_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// paramsRecorder is an ActionExecutor that keeps the params each task
// reached it with, which are what dispatch hands a method.
type paramsRecorder struct{ got map[string]any }

func (p *paramsRecorder) Execute(_ context.Context, task *engine.Task, _ inventory.InventoryItem) (engine.ActionResult, error) {
	p.got = task.Params
	return engine.ActionResult{}, nil
}

// registerTypedMethod registers t118.typed, declaring one parameter of each
// shape a rendered value is held to.
func registerTypedMethod(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{Name: "t118.typed", Manifest: collection.Manifest{Status: collection.StatusDeclared,
		Doc: collection.Doc{Params: []collection.Param{
			{Name: "stdin", Type: "string"},
			{Name: "args", Type: "list of string"},
			{Name: "count", Type: "int"},
			{Name: "on", Type: "bool"},
			{Name: "ports", Type: "scalar or list"},
		}}}}); err != nil {
		t.Fatal(err)
	}
}

// runTyped runs one t118.typed task with params, rendering against vars,
// and returns what the method received and the task's error.
func runTyped(t *testing.T, params string, vars map[string]any) (map[string]any, error) {
	t.Helper()
	rec := &paramsRecorder{}
	x := engine.NewExecutor(mapResolver{}, rec, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithVariables(vars), engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, "id: typed\ntasks:\n  - name: typed\n    t118.typed:\n"+params))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rec.got, result.Nodes[0].Err
}

// TestRenderedParams_AStringParameterReceivesText: a parameter declared a
// string receives the text of what its template renders, a number and a
// boolean included, and so does each item of a list of strings. Kept as the
// number, stdin: "{{ vars.n }}" reached exec.shell as nothing, since its
// string reader takes any other type for absent (found writing the lab's
// disk cleanup runbook, 2026-10-19).
func TestRenderedParams_AStringParameterReceivesText(t *testing.T) {
	registerTypedMethod(t)
	got, err := runTyped(t, "      stdin: \"{{ vars.n }}\"\n      args: [\"{{ vars.n }}\", \"{{ vars.flag }}\", plain]\n",
		map[string]any{"n": 7, "flag": true})
	if err != nil {
		t.Fatalf("the task failed: %v", err)
	}
	if got["stdin"] != "7" {
		t.Errorf("stdin = %#v, want the text 7", got["stdin"])
	}
	if want := []any{"7", "true", "plain"}; !reflect.DeepEqual(got["args"], want) {
		t.Errorf("args = %#v, want %#v", got["args"], want)
	}
}

// TestRenderedParams_OtherTypesKeepTheirValue: a parameter of any other type
// receives the rendered value itself, a number as a number and a list as a
// list, where it is of the declared type.
func TestRenderedParams_OtherTypesKeepTheirValue(t *testing.T) {
	registerTypedMethod(t)
	got, err := runTyped(t, "      count: \"{{ vars.n }}\"\n      on: \"{{ vars.flag }}\"\n      ports: \"{{ vars.ports }}\"\n",
		map[string]any{"n": 7, "flag": true, "ports": []any{22, "8000-8100"}})
	if err != nil {
		t.Fatalf("the task failed: %v", err)
	}
	if got["count"] != 7 || got["on"] != true || !reflect.DeepEqual(got["ports"], []any{22, "8000-8100"}) {
		t.Errorf("received %#v", got)
	}
}

// TestRenderedParams_AValueOfAnotherTypeIsRefused: a rendered value that is
// not of the declared type fails the task naming the parameter, its type and
// what it rendered to, before the method runs; so does a list or a map
// rendered into a string, whose text nobody chose. The value is never named.
func TestRenderedParams_AValueOfAnotherTypeIsRefused(t *testing.T) {
	registerTypedMethod(t)
	for _, tc := range []struct {
		name, params string
		want         []string
	}{
		{"text into an int", "      count: \"{{ vars.word }}\"\n", []string{"parameter count", "a string", "declares it int"}},
		{"a list into a string", "      stdin: \"{{ vars.list }}\"\n", []string{"parameter stdin", "a list", "takes text"}},
		{"a map into an item of a list of strings", "      args: [\"{{ vars.map }}\"]\n", []string{"parameter args", "a map"}},
		{"a boolean into ports", "      ports: \"{{ vars.flag }}\"\n", []string{"parameter ports", "a boolean", "declares it scalar or list"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runTyped(t, tc.params, map[string]any{"word": "hunter2", "list": []any{"a"}, "map": map[string]any{"k": "v"}, "flag": true})
			if err == nil {
				t.Fatalf("the task ran with %#v", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to say %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("the error carries the value: %v", err)
			}
			if got != nil {
				t.Errorf("the method ran with %#v", got)
			}
		})
	}
}
