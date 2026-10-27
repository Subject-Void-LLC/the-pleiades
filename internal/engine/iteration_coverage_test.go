// Tests of the edges of loops, retries, assert and the project template
// reader that the main tests do not reach: the node id a journal entry names,
// the shapes a loop or an assert refuses, and what a loop does with a list it
// cannot use.
package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestBaseNodeIDAndNodeFor(t *testing.T) {
	for id, want := range map[string]string{
		"tasks[0]":                             "tasks[0]",
		"tasks[1].item[2]":                     "tasks[1]",
		"tasks[1].attempt[0]":                  "tasks[1]",
		"tasks[1].block[0].item[3].attempt[1]": "tasks[1].block[0]",
		"tasks[1].undo[2]":                     "tasks[1].undo[2]",
		"tasks[1].item[2].undo[0]":             "tasks[1].item[2].undo[0]",
	} {
		if got := engine.BaseNodeID(id); got != want {
			t.Errorf("BaseNodeID(%q) = %q, want %q", id, got, want)
		}
	}
	dag := yamlRunbook(t, "id: n\ntasks:\n  - noop: {}\n    loop: [1]\n")
	if task, ok := dag.NodeFor("tasks[0].item[0]"); !ok || task != dag.Nodes["tasks[0]"] {
		t.Errorf("NodeFor did not find the looped task: %v %v", task, ok)
	}
	if _, ok := dag.NodeFor("tasks[9].item[0]"); ok {
		t.Error("NodeFor found a task that is not there")
	}
}

func TestLoop_RefusesWhatItCannotUse(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"renders to text":     {"loop: \"{{ vars.word }}\"", "needs a list"},
		"label cannot render": {"loop: [1]\n    loop_control: {label: \"{{ vars.nope.deeper }}\"}", "label"},
		"undefined list":      {"loop: \"{{ vars.nope }}\"", "loop"},
	} {
		t.Run(name, func(t *testing.T) {
			_, result, _ := runStepper(t, collection.ModeExecute, "id: l\nhosts: web\nvars:\n  word: hello\ntasks:\n  - rbtest.step:\n      note: x\n    "+tc.body+"\n", echo)
			if !result.HasErrors() {
				t.Fatal("the task did not fail")
			}
			if msg := result.Nodes[0].Err.Error(); !strings.Contains(msg, tc.want) {
				t.Errorf("error %q lacks %q", msg, tc.want)
			}
		})
	}
	var items strings.Builder
	for i := 0; i <= engine.MaxLoopItems; i++ {
		items.WriteString("1,")
	}
	eval, _ := engine.NewCELEvaluator()
	if _, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: big\nhosts: web\ntasks:\n  - noop: {}\n    loop: [" + items.String() + "]\n")); err == nil {
		t.Error("a loop over the limit built")
	}
	// A list that renders larger than the limit is refused when it renders.
	big := make([]any, engine.MaxLoopItems+1)
	_, result, _ := runStepper(t, collection.ModeExecute, "id: big2\nhosts: web\ntasks:\n  - rbtest.step:\n      note: x\n    loop: \"{{ vars.big }}\"\n", echo, big)
	if !result.HasErrors() || !strings.Contains(result.Nodes[0].Err.Error(), "at most") {
		t.Errorf("a rendered list over the limit: %+v", result.Nodes)
	}
}

func TestAssert_Shapes(t *testing.T) {
	_, result, _ := runStepper(t, collection.ModeExecute, "id: a\nhosts: web\ntasks:\n  - assert:\n      that: \"1 == 1\"\n", echo)
	if result.HasErrors() {
		t.Fatalf("an assert with that as one string: %+v", result.Nodes)
	}
	_, result, _ = runStepper(t, collection.ModeExecute, "id: a2\nhosts: web\ntasks:\n  - assert:\n      that: [\"1 == 2\", \"1 == 1\"]\n", echo)
	if !result.HasErrors() || !strings.Contains(result.Nodes[0].Err.Error(), "assertion failed: 1 == 2 and 1 == 1") {
		t.Fatalf("a false assert without fail_msg: %+v", result.Nodes)
	}
	_, result, _ = runStepper(t, collection.ModeExecute, "id: f\nhosts: web\ntasks:\n  - fail: {}\n", echo)
	if !result.HasErrors() || !strings.Contains(result.Nodes[0].Err.Error(), "failed as requested from task") {
		t.Fatalf("fail without msg: %+v", result.Nodes)
	}
	for name, body := range map[string]string{
		"empty text": "assert: {that: \"\"}",
		"empty list": "assert: {that: []}",
		"non text":   "assert: {that: [1]}",
		"wrong type": "assert: {that: 5}",
	} {
		eval, _ := engine.NewCELEvaluator()
		if _, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: bad\ntasks:\n  - " + body + "\n")); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestUntil_ARegisterlessNothingToPatchAndAFailingConditionNamesItself(t *testing.T) {
	// A condition that errors on a try that succeeded is the task's failure.
	_, result, _ := runStepper(t, collection.ModeExecute, `id: bad
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    until: ["result.r.missing.deeper == 1"]
    retries: 1
    delay: 0
`, echo)
	if !result.HasErrors() {
		t.Fatal("an until that cannot evaluate did not fail the task")
	}
	found := false
	for _, n := range result.Nodes {
		if n.Err != nil && strings.Contains(n.Err.Error(), "until") {
			found = true
		}
	}
	if !found {
		t.Errorf("no error names until: %+v", result.Nodes)
	}
}

func TestProjectTemplateFiles_ReadsOnlyInsideTheProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.tmpl"), []byte("hello {{ vars.x }}"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := engine.ProjectTemplateFiles(dir)
	got, err := read("t.tmpl", 1024)
	if err != nil || string(got) != "hello {{ vars.x }}" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if _, err := read("../outside", 1024); err == nil {
		t.Error("a path outside the project was read")
	}
	if _, err := read("t.tmpl", 3); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file over the limit: %v", err)
	}
}

// TestUntil_OnlyTheMethodsOwnFailureIsTriedAgain: a failure that is not the
// method's (here a render error on the first try's parameters) is not retried,
// so a refused login is never tried again and again.
func TestUntil_OnlyTheMethodsOwnFailureIsTriedAgain(t *testing.T) {
	s, result, _ := runStepper(t, collection.ModeExecute, `id: nr
hosts: web
tasks:
  - rbtest.step:
      note: "{{ vars.undefined_name }}"
    register: r
    until: ["true == false"]
    retries: 5
    delay: 0
`, echo)
	if !result.HasErrors() || len(s.calls) != 0 || len(result.Nodes) != 1 {
		t.Fatalf("a render failure was retried: calls %v, %d results", s.calls, len(result.Nodes))
	}
}

// TestUntil_AStoppedRunIsNotASuccess: a run canceled while an until task waits
// to try again ends failed and returns the cancellation, never a clean result.
func TestUntil_AStoppedRunIsNotASuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &stepper{fn: func(int, *engine.Task) (engine.ActionResult, error) {
		cancel()
		return engine.ActionResult{Stats: map[string]any{"ok": false}}, nil
	}, builtin: engine.NewBuiltinActionExecutor()}
	x := engine.NewExecutor(oneDevice(), s, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()))
	result, err := x.Run(ctx, yamlRunbook(t, `id: stop
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    until: ["result.r.ok == true"]
    retries: 3
    delay: 30
`))
	if err == nil {
		t.Fatalf("a canceled run returned no error: %+v", result.Nodes)
	}
	if !result.HasErrors() {
		t.Errorf("the try that was waiting to be repeated is not the task's failure: %+v", result.Nodes)
	}
}

func TestLoop_RunsAreBounded(t *testing.T) {
	var items strings.Builder
	for i := 0; i < 100; i++ {
		items.WriteString("1,")
	}
	_, result, _ := runStepper(t, collection.ModeExecute, `id: cap
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    loop: [`+items.String()+`]
    until: ["false"]
    retries: 100
    delay: 0
`, echo)
	if !result.HasErrors() || !strings.Contains(result.Nodes[0].Err.Error(), "at most") {
		t.Fatalf("10100 runs on one device were allowed: %+v", result.Nodes[0])
	}
}

func TestIteration_RefusedCombinations(t *testing.T) {
	for name, body := range map[string]string{
		"global fact in a loop": "set_fact: {x: 1, global: true}\n    loop: [1, 2]",
	} {
		t.Run(name, func(t *testing.T) {
			eval, _ := engine.NewCELEvaluator()
			if _, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: bad\nhosts: web\ntasks:\n  - " + body + "\n")); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// TestUntil_ReadsItsOwnResultWhenOtherDevicesRegisteredToo: with two devices
// writing one register, result.<register> would be left out of a condition
// unless the task's own result is put there, so a retry on each device reads
// its own.
func TestUntil_ReadsItsOwnResultWhenOtherDevicesRegisteredToo(t *testing.T) {
	varsFixtures.Do(func() {
		registerRollbackFixtures(t)
		registerDeviceRootTypes()
	})
	seen := &stepper{fn: func(n int, _ *engine.Task) (engine.ActionResult, error) {
		return engine.ActionResult{Stats: map[string]any{"up": true}}, nil
	}, builtin: engine.NewBuiltinActionExecutor()}
	x := engine.NewExecutor(varsDevices(nil, nil), seen, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, `id: two
hosts: web
tasks:
  - rbtest.step: {note: x}
    register: r
    until: ["result.r.up == true"]
    retries: 2
    delay: 0
`))
	if err != nil || result.HasErrors() {
		t.Fatalf("until on two devices: %v %+v", err, result.Nodes)
	}
}

// TestProjectPath_TakesTheItemOfALoopTheAuthorWrote: a parameter that names a
// project file may read the item of a loop written in the runbook or read
// from vars, which are the author's, and never the item of a loop read from a
// device's output.
func TestProjectPath_TakesTheItemOfALoopTheAuthorWrote(t *testing.T) {
	for loop, wantRefusal := range map[string]bool{
		"[a, b]":                       false,
		"\"{{ vars.sites }}\"":         false,
		"\"{{ result.found.names }}\"": true,
	} {
		eval, _ := engine.NewCELEvaluator()
		dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: p\nvars:\n  sites: [a, b]\ntasks:\n  - noop: {}\n    register: found\n  - pleiades.builtin.project.write:\n      path: \"captures/{{ item }}.txt\"\n      content: x\n    loop: " + loop + "\n"))
		if err != nil {
			t.Fatalf("loop %s: %v", loop, err)
		}
		task := dag.Nodes["tasks[1]"]
		problems := false
		for _, src := range engine.TemplateStrings(task.Params["path"]) {
			tmpl, cerr := render.New().Compile(src)
			if cerr != nil {
				t.Fatal(cerr)
			}
			problems = problems || len(engine.TaskTemplateProblems(tmpl, collection.ParamFormatProjectPath, task)) > 0
		}
		if problems != wantRefusal {
			t.Errorf("loop %s: refused = %v, want %v", loop, problems, wantRefusal)
		}
	}
}
