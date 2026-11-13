// Tests that the planner replays an authored rollback: step's templated
// parameters from the values the run journaled for them (rendered.go), refuses
// by name one the journal cannot answer for, and that Verify holds such a
// step to the runbook and the declarations.
package rollback_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// templatedStep is the runbook's undo of tasks[0]: rb.delete of the name the
// run's variable held, with a literal uuid.
func templatedStep() engine.Task {
	return engine.Task{Name: "remove", FQCN: "rb.delete", Params: map[string]any{"name": "{{ vars.where }}-vm", "uuid": "u-1"}}
}

// renderedRun is a run whose tasks[0] on h1 made something and journaled its
// templated undo step as rendered, with rec in place of the complete record
// when rec is not nil.
func renderedRun(rec *engine.AuthoredStep) rollback.Run {
	e := entry("run-A", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged)
	e.AuthoredRollback = true
	name := "bsd-first-vm"
	step := engine.AuthoredStep{Index: 0, FQCN: "rb.delete", StepDigest: engine.AuthoredStepDigest(templatedStep()),
		Params: []engine.InverseParam{{Key: "name", Text: &name}}, Complete: true}
	if rec != nil {
		step = *rec
	}
	e.AuthoredSteps = []engine.AuthoredStep{step}
	return rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{e}}
}

// renderedRequest asks for a rollback of run whose runbook's tasks[0] has
// steps as its rollback: list.
func renderedRequest(run rollback.Run, steps ...engine.Task) rollback.Request {
	return rollback.Request{Target: run, Devices: inventoryOf("h1"), Authored: func(string) ([]engine.Task, error) { return steps, nil }}
}

// TestBuild_ReplaysATemplatedStepAsTheRunRenderedIt: the step's templated name
// is the value the run journaled, its literal uuid the runbook's, and the plan
// says which parameter was rendered; a step with no template is run as written.
func TestBuild_ReplaysATemplatedStepAsTheRunRenderedIt(t *testing.T) {
	registerShapes(t)
	literal := engine.Task{Name: "stop", FQCN: "rb.stop", Params: map[string]any{"name": "svc"}}
	run := renderedRun(nil)
	// The literal step is first in the list, so the templated one is index 1.
	run.Entries[0].AuthoredSteps[0].Index = 1
	plan, err := rollback.Build(renderedRequest(run, literal, templatedStep()))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	steps := plan.Steps()
	if len(steps) != 2 {
		t.Fatalf("steps %+v, want two", steps)
	}
	if steps[0].Params["name"] != "svc" || len(steps[0].Rendered) != 0 {
		t.Errorf("the literal step %+v is not run as written", steps[0])
	}
	got := steps[1]
	if got.Params["name"] != "bsd-first-vm" || got.Params["uuid"] != "u-1" || len(got.Rendered) != 1 || got.Rendered[0] != "name" {
		t.Errorf("the templated step %+v does not carry the journaled name beside the runbook's uuid", got)
	}
}

// TestBuild_RefusesATemplatedStepTheJournalCannotAnswerFor: a step journaled
// with no value, one the journal holds no copy of, one edited since the run,
// and one carrying a value the declaration does not record are each refused by
// name, before anything runs, saying to undo it by hand; --leave accepts it. A
// step reading only the device, which a rollback has, is still run as written.
func TestBuild_RefusesATemplatedStepTheJournalCannotAnswerFor(t *testing.T) {
	registerShapes(t)
	content := "old text"
	edited := templatedStep()
	edited.Params["name"] = "{{ vars.where }}-other"
	for _, tc := range []struct {
		name string
		run  rollback.Run
		step engine.Task
		want string
	}{
		{name: "withheld for a secret", step: templatedStep(), want: "holds one of the run's secrets",
			run: renderedRun(&engine.AuthoredStep{Index: 0, FQCN: "rb.delete", StepDigest: engine.AuthoredStepDigest(templatedStep()), Withheld: engine.WithheldSecret})},
		{name: "no rendered copy", step: templatedStep(), want: "holds no rendered copy",
			run: func() rollback.Run { r := renderedRun(nil); r.Entries[0].AuthoredSteps = nil; return r }()},
		{name: "edited since the run", step: edited, run: renderedRun(nil), want: "changed since the run"},
		{name: "a value the declaration does not record", want: "does not declare recordable",
			step: engine.Task{FQCN: "rb.delete", Params: map[string]any{"content": "{{ vars.where }}"}},
			run: renderedRun(&engine.AuthoredStep{Index: 0, FQCN: "rb.delete", Complete: true,
				StepDigest: engine.AuthoredStepDigest(engine.Task{FQCN: "rb.delete", Params: map[string]any{"content": "{{ vars.where }}"}}),
				Params:     []engine.InverseParam{{Key: "content", Text: &content}}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := renderedRequest(tc.run, tc.step)
			_, err := rollback.Build(req)
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Node != "tasks[0]" || problems[0].DeviceID != "h1" ||
				!strings.Contains(problems[0].Reason, tc.want) || !strings.Contains(problems[0].Reason, "by hand") || problems[0].Accept != "--leave tasks[0]" {
				t.Fatalf("problems %+v, want tasks[0] on h1 refused saying %q, accepted by --leave", problems, tc.want)
			}
			req.Leave = map[string]bool{"tasks[0]": true}
			if _, err := rollback.Build(req); err != nil {
				t.Errorf("--leave did not accept it: %v", err)
			}
		})
	}

	deviceOnly := engine.Task{FQCN: "rb.delete", Params: map[string]any{"name": "{{ device.name }}-vm"}}
	run := renderedRun(nil)
	run.Entries[0].AuthoredSteps = nil
	plan, err := rollback.Build(renderedRequest(run, deviceOnly))
	if err != nil {
		t.Fatalf("a step reading only the device was refused: %v", err)
	}
	if s := plan.Steps()[0]; s.Params["name"] != "{{ device.name }}-vm" || len(s.Rendered) != 0 {
		t.Errorf("the device-only step %+v is not run as written", s)
	}
}

// TestBuild_RefusesARecordedUndoWithheldForASecret: a method's recorded undo
// whose value the journal kept out for holding a secret is refused by name
// before anything runs, saying so, and only --leave accepts it.
func TestBuild_RefusesARecordedUndoWithheldForASecret(t *testing.T) {
	registerShapes(t)
	e := undo(entry("run-A", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"uuid": "u-1"})
	e.InverseParamKeys = []string{"name", "uuid"}
	e.InverseComplete, e.InverseWithheld = false, engine.WithheldSecret
	req := rollback.Request{Target: rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{e}}, Devices: inventoryOf("h1")}
	_, err := rollback.Build(req)
	problems := refused(t, err)
	if len(problems) != 1 || problems[0].Node != "tasks[0]" || problems[0].DeviceID != "h1" || problems[0].Accept != "--leave tasks[0]" ||
		!strings.Contains(problems[0].Reason, "holds one of the run's secrets") || !strings.Contains(problems[0].Reason, "by hand") {
		t.Fatalf("problems %+v, want tasks[0] on h1 refused for the secret, accepted by --leave", problems)
	}
	req.Leave = map[string]bool{"tasks[0]": true}
	if _, err := rollback.Build(req); err != nil {
		t.Errorf("--leave did not accept it: %v", err)
	}
}

// renderedRunbook is the runbook renderedRun ran.
const renderedRunbook = `id: ran
hosts: h1
tasks:
  - name: make
    rb.make: {name: bsd}
    rollback:
      - name: remove
        rb.delete: {name: "{{ vars.where }}-vm", uuid: u-1}
`

// TestVerify_HoldsARenderedStepToTheRunbookAndTheDeclarations: the planned
// step passes; one whose literal parameter differs from the runbook, one
// claiming a rendered value for a literal, one carrying a value the journal
// would never hold, and one leaving a template unrendered are refused.
func TestVerify_HoldsARenderedStepToTheRunbookAndTheDeclarations(t *testing.T) {
	registerShapes(t)
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(renderedRunbook))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := rollback.Build(renderedRequest(renderedRun(nil), templatedStep()))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	good := plan.Steps()[0]
	if err := rollback.Verify(dag, good); err != nil {
		t.Fatalf("Verify refused the planned step: %v", err)
	}
	for name, mutate := range map[string]func(s *rollback.Step){
		"a literal changed":         func(s *rollback.Step) { s.Params["uuid"] = "u-2" },
		"a literal claimed":         func(s *rollback.Step) { s.Rendered = append(s.Rendered, "uuid") },
		"a value never held":        func(s *rollback.Step) { s.Params["name"] = "a\nb" },
		"a template left in place":  func(s *rollback.Step) { s.Params["name"] = "{{ vars.where }}-vm"; s.Rendered = []string{"uuid"} },
		"a parameter the run added": func(s *rollback.Step) { s.Params["size"] = 3 },
	} {
		t.Run(name, func(t *testing.T) {
			s := good
			s.Params = map[string]any{}
			for k, v := range good.Params {
				s.Params[k] = v
			}
			s.Rendered = append([]string(nil), good.Rendered...)
			mutate(&s)
			if err := rollback.Verify(dag, s); err == nil {
				t.Errorf("Verify accepted %+v", s)
			}
		})
	}
}
