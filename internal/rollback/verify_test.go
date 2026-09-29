// Tests for holding a rollback step to the runbook that ran (verify.go) and
// for the runbook a plan runs as (runbook.go).
package rollback_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// ranRunbook is the runbook a run being undone ran: a make with a
// recorded undo, and a command whose undo is its rollback: list.
const ranRunbook = `id: ran
hosts: h1
tasks:
  - name: make it
    rb.make:
      name: bsd
  - name: run a command
    rb.shell:
      name: marker
    rollback:
      - name: stop it
        rb.stop:
          name: marker
      - name: clean up
        rb.delete:
          name: marker
          size: 3
`

func buildRan(t *testing.T) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(ranRunbook))
	if err != nil {
		t.Fatalf("building the runbook: %v", err)
	}
	return dag
}

// throughJSON is s as a Runner decodes it from a dispatch payload.
func throughJSON(t *testing.T, s rollback.Step) rollback.Step {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var out rollback.Step
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerify_HoldsAStepToTheRunbookThatRan(t *testing.T) {
	registerShapes(t)
	dag := buildRan(t)
	recorded := rollback.Step{Node: "tasks[0]", Emitter: "rb.make", FQCN: "rb.delete", Params: map[string]any{"name": "bsd", "uuid": "u-1"}, Source: rollback.SourceRecorded}
	authored := rollback.Step{Node: "tasks[1]", Index: 1, Emitter: "rb.shell", FQCN: "rb.delete", Params: map[string]any{"name": "marker", "size": 3}, Source: rollback.SourceAuthored}
	for _, s := range []rollback.Step{recorded, authored, throughJSON(t, recorded), throughJSON(t, authored)} {
		if err := rollback.Verify(dag, s); err != nil {
			t.Errorf("Verify(%+v) = %v, want it accepted", s, err)
		}
	}

	for name, tc := range map[string]struct {
		edit func(*rollback.Step)
		base rollback.Step
		want string
	}{
		"a node the runbook lacks":          {base: recorded, edit: func(s *rollback.Step) { s.Node = "tasks[9]" }, want: "no node"},
		"another method at that node":       {base: recorded, edit: func(s *rollback.Step) { s.Emitter = "rb.start" }, want: "runs rb.make"},
		"an undo the method never declares": {base: recorded, edit: func(s *rollback.Step) { s.FQCN = "rb.stop" }, want: "does not declare"},
		"a withheld value":                  {base: recorded, edit: func(s *rollback.Step) { s.Params["content"] = "x" }, want: "not declare recordable"},
		"a device selector":                 {base: recorded, edit: func(s *rollback.Step) { s.Params["target"] = "elsewhere" }, want: "not declare recordable"},
		"an escape sequence":                {base: recorded, edit: func(s *rollback.Step) { s.Params["name"] = "bsd\x1b[2J" }, want: "never hold"},
		"a value no journal holds":          {base: recorded, edit: func(s *rollback.Step) { s.Params["name"] = []any{"a"} }, want: "never hold"},
		"a second recorded step":            {base: recorded, edit: func(s *rollback.Step) { s.Index = 1 }, want: "one step"},
		"an edited authored step":           {base: authored, edit: func(s *rollback.Step) { s.Params["name"] = "prod" }, want: "not what the runbook says"},
		"another authored method":           {base: authored, edit: func(s *rollback.Step) { s.FQCN = "rb.stop" }, want: "not what the runbook says"},
		"an authored step past the list":    {base: authored, edit: func(s *rollback.Step) { s.Index = 2 }, want: "has 2 rollback"},
		"an authored step at a plain node":  {base: authored, edit: func(s *rollback.Step) { s.Node, s.Emitter, s.Index = "tasks[0]", "rb.make", 0 }, want: "has 0 rollback"},
		"no source":                         {base: recorded, edit: func(s *rollback.Step) { s.Source = "" }, want: "neither"},
	} {
		t.Run(name, func(t *testing.T) {
			s := tc.base
			s.Params = map[string]any{}
			for k, v := range tc.base.Params {
				s.Params[k] = v
			}
			tc.edit(&s)
			err := rollback.Verify(dag, s)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Verify = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestRunbook_WritesEachLevelAsATaskAndSaysWhatItUndoes(t *testing.T) {
	registerShapes(t)
	levels := [][]rollback.Step{
		{{Node: "tasks[1]", Index: 0, FQCN: "rb.stop", Params: map[string]any{"name": "a: b\n- c"}, Name: "undo tasks[1]", DeviceName: "h1"}},
		{
			{Node: "tasks[0]", FQCN: "rb.delete", Params: map[string]any{"name": "x"}, Name: "undo tasks[0]", DeviceName: "h1"},
			{Node: "tasks[0]", FQCN: "rb.delete", Params: map[string]any{"name": "y"}, Name: "undo tasks[0]", DeviceName: "h2"},
		},
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, targets := range []bool{true, false} {
		payload, undoes, err := rollback.Runbook("rollback-x", levels, targets)
		if err != nil {
			t.Fatal(err)
		}
		dag, err := engine.NewBuilder(eval).BuildFromYAML(payload)
		if err != nil {
			t.Fatalf("the rollback runbook does not build: %v\n%s", err, payload)
		}
		if got := dag.Nodes["tasks[0]"].Params["name"]; got != "a: b\n- c" {
			t.Errorf("a value shaped like YAML came back as %q: it changed the runbook's shape", got)
		}
		_, hasTarget := dag.Nodes["tasks[1].parallel[1]"].Params["target"]
		if hasTarget != targets {
			t.Errorf("targets %v: the second device's task has a target: %v", targets, hasTarget)
		}
		if undoes["tasks[0]"] != (engine.Undo{Node: "tasks[1]"}) || undoes["tasks[1].parallel[1]"] != (engine.Undo{Node: "tasks[0]"}) || len(undoes) != 3 {
			t.Errorf("undoes %+v", undoes)
		}
	}
}
