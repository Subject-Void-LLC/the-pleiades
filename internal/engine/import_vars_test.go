// Tests of import_tasks as a function (Phase 87): the importing task's vars:
// are the arguments of the imported file, which reads them as vars, and a
// call's arguments reach it, with a default written by the default filter.
package engine_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

func TestImportTasks_VarsAreTheCallsArguments(t *testing.T) {
	varsFixtures.Do(func() {
		registerRollbackFixtures(t)
		registerDeviceRootTypes()
	})
	dir := t.TempDir()
	writeRunbookFile(t, dir, "greet.yaml", `
- name: say it
  rbtest.make:
    name: x
    note: "{{ vars.greeting | default('hello') }} {{ vars.who }}"
`)
	main := writeRunbookFile(t, dir, "main.yaml", `
id: fn
hosts: web
tasks:
  - name: greet the world
    import_tasks:
      file: greet.yaml
    vars:
      who: world
  - name: greet with a different greeting
    import_tasks:
      file: greet.yaml
    vars:
      who: there
      greeting: hi
`)
	dag, err := newTestBuilder(t).BuildFromYAMLFile(main)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	seen := &paramsSeen{}
	x := engine.NewExecutor(varsDevices(nil, nil), seen, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), dag)
	if err != nil || result.HasErrors() {
		t.Fatalf("run: %v %+v", err, result.Nodes)
	}
	slices.Sort(seen.seen)
	// A default is written with the default filter, since a vars: the imported
	// task set for itself would be the narrower scope and beat the call's.
	want := []string{"a: hello world", "a: hi there", "b: hello world", "b: hi there"}
	if !slices.Equal(seen.seen, want) {
		t.Fatalf("saw %v, want %v", seen.seen, want)
	}
}
