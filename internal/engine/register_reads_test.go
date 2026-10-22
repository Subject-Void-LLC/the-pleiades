// Tests for TaskReads and TemplateReads, the one answer to which registers a
// task reads.
package engine_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestTaskReads: every place a task reads a register is counted (each
// condition keyword, each root and spelling, a template anywhere in params,
// secret_mask), a whole root counts as reading everything, and vars, error
// and a rollback: list read no register.
func TestTaskReads(t *testing.T) {
	eng := render.New()
	for _, tc := range []struct {
		name  string
		task  engine.Task
		names []string
		all   bool
	}{
		{"nothing", engine.Task{FQCN: "noop"}, nil, false},
		{"when", engine.Task{Conditional: engine.Conditional{When: []string{"result.a.rc == 0"}}}, []string{"a"}, false},
		{"when_or and when_cel", engine.Task{Conditional: engine.Conditional{WhenOr: []string{"stat.b.x"}, WhenCEL: "nodes['c'].y"}}, []string{"b", "c"}, false},
		{"a computed index", engine.Task{Conditional: engine.Conditional{When: []string{"nodes[vars.k].x"}}}, nil, true},
		{"a param template", engine.Task{Params: map[string]any{"body": "{{ result.d.json | to_json }}"}}, []string{"d"}, false},
		{"a nested param template", engine.Task{Params: map[string]any{"list": []any{map[string]any{"x": "{{ nodes.e.f }}"}}}}, []string{"e"}, false},
		{"a whole root in a template", engine.Task{Params: map[string]any{"body": "{{ nodes | to_json }}"}}, nil, true},
		{"vars and error read no register", engine.Task{Params: map[string]any{"a": "{{ vars.x }}", "b": "{{ error.msg }}"}}, nil, false},
		{"secret_mask", engine.Task{SecretMask: &engine.SecretMaskSpec{Register: "g", Fields: []string{"token"}}}, []string{"g"}, false},
		{"rollback steps are read", engine.Task{Rollback: []engine.Task{{Params: map[string]any{"x": "{{ result.h.y }}"}}, {Params: map[string]any{"z": "{{ nodes | to_json }}"}}}}, []string{"h"}, true},
		{"a literal tested with in", engine.Task{Conditional: engine.Conditional{When: []string{"\"j\" in stat"}}}, []string{"j"}, false},
		{"one name from three places", engine.Task{
			Conditional: engine.Conditional{When: []string{"result.i.rc == 0"}},
			Params:      map[string]any{"x": "{{ result.i.y }}"},
			SecretMask:  &engine.SecretMaskSpec{Register: "i"},
		}, []string{"i"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := engine.TaskReads(eng, &tc.task)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Names, tc.names) && !(len(got.Names) == 0 && len(tc.names) == 0) {
				t.Errorf("Names = %v, want %v", got.Names, tc.names)
			}
			if got.All != tc.all {
				t.Errorf("All = %v, want %v", got.All, tc.all)
			}
		})
	}
}

// TestTaskReads_RefusesWhatDoesNotParse: a condition or a template that does
// not compile is an error, never a task that reads nothing.
func TestTaskReads_RefusesWhatDoesNotParse(t *testing.T) {
	eng := render.New()
	for name, task := range map[string]engine.Task{
		"condition": {Conditional: engine.Conditional{When: []string{"result.a.rc =="}}},
		"template":  {Params: map[string]any{"x": "{{ result.a | nosuchfilter }}"}},
		"rollback":  {Rollback: []engine.Task{{Conditional: engine.Conditional{When: []string{"x =="}}}}},
	} {
		if _, err := engine.TaskReads(eng, &task); err == nil {
			t.Errorf("%s: TaskReads accepted what does not parse", name)
		}
	}
}
