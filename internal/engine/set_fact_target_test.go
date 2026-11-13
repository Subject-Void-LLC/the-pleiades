// Tests that a set_fact's target is the task's target and never a fact,
// through the real Executor.
package engine_test

import (
	"slices"
	"testing"
)

// TestSetFact_TargetIsTheTasksTargetNeverAFact: a set_fact's target names the
// devices it runs on, as on any task, and sets no variable named target.
// Before the fix the engine read it both ways at once: the task ran only on
// the device the value named, and that device also got a fact called target.
func TestSetFact_TargetIsTheTasksTargetNeverAFact(t *testing.T) {
	resolver := varsDevices(nil, nil)
	resolver["b"] = resolver["web"][1:]
	seen, result := runVars(t, resolver, nil, `id: targeted
hosts: web
tasks:
  - set_fact:
      target: b
      x: one
  - rbtest.make:
      name: r
      note: "{{ vars.x | default('unset') }} {{ vars.target | default('unset') }}"
`)
	want := []string{"a: unset unset", "b: one unset"}
	if result.HasErrors() || !slices.Equal(seen, want) {
		t.Fatalf("saw %v, want %v, %+v", seen, want, result.Nodes)
	}
}
