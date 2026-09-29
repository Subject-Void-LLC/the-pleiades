// Package validate_test: tests of the rollback: and reversible: rules.
package validate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerUndoShapes registers one fixture method for each reversibility
// shape ReversibleRule tells apart, plus the target their undos call.
func registerUndoShapes(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	target := sdk.InverseSpec{FQCN: "undo.target", Record: []string{"name"}}
	for name, r := range map[string]collection.Reversibility{
		"undo.target":      {Notes: "fixture"},
		"undo.whole":       {Reversible: true, Inverses: []sdk.InverseSpec{target}},
		"undo.withholding": {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "undo.target", Record: []string{"name"}, Withhold: []string{"content"}}}},
		"undo.partial":     {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "undo.target", Record: []string{"name"}, MayBePartial: true}}},
		"undo.undeclared":  {Reversible: true},
		"undo.never":       {Notes: "What it deletes is gone."},
		"undo.reads":       {Notes: "Reading is not changing.", ReadOnly: true},
	} {
		d := collection.Descriptor{Name: name, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: r,
			Doc: collection.Doc{Params: []collection.Param{{Name: "name"}, {Name: "content"}}},
		}, Invoke: run}
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	// An external program's method, whose declaration is never honored.
	external := collection.Descriptor{Name: "undo.external", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{target}},
		Doc: collection.Doc{Params: []collection.Param{{Name: "name"}}},
	}, Invoke: run, Provider: &collection.Provider{Program: "/opt/collections/undo", Digest: "sha256:" + strings.Repeat("0", 64)}}
	if err := collection.Register(external); err != nil {
		t.Fatal(err)
	}
}

// reversibleRunbook is a reversible runbook with one task calling method.
func reversibleRunbook(method, extra string) string {
	return "id: r\nhosts: web\nreversible: true\ntasks:\n  - name: step\n    " + method + ":\n      name: x\n" + extra
}

func TestReversibleRule_RefusesATaskNoRollbackCouldUndo(t *testing.T) {
	registerUndoShapes(t)
	for method, want := range map[string]string{
		"undo.whole":       "",
		"undo.reads":       "",
		"noop":             "",
		"undo.never":       "cannot be undone (What it deletes is gone)",
		"undo.withholding": "keeps content out of the journal",
		"undo.partial":     "can leave part of what it changed in place",
		"undo.undeclared":  "recorded by name only",
		"ssh_exec":         "arbitrary command",
		"undo.external":    "an external program's method",
		// Nothing registers it: CollectionRule's finding, not this rule's.
		"undo.nobody": "",
	} {
		t.Run(method, func(t *testing.T) {
			world := validate.WorldView{DAG: yamlDAG(t, reversibleRunbook(method, ""))}
			findings := validate.ReversibleRule(world)
			switch {
			case want == "" && len(findings) != 0:
				t.Errorf("%s refused: %+v", method, findings)
			case want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, want) || findings[0].Node != "tasks[0]"):
				t.Errorf("%s: findings %+v, want one on tasks[0] mentioning %q", method, findings, want)
			}
		})
	}
}

// TestReversibleRule_AnAuthoredUndoAnswersForAnyMethod, and a runbook that
// never asked is never refused: the rule holds a promise, it does not
// impose one.
func TestReversibleRule_AnAuthoredUndoAnswersForAnyMethod(t *testing.T) {
	registerUndoShapes(t)
	authored := reversibleRunbook("undo.never", "    rollback:\n      - undo.target:\n          name: x\n")
	if findings := validate.ReversibleRule(validate.WorldView{DAG: yamlDAG(t, authored)}); len(findings) != 0 {
		t.Errorf("a task with a rollback: list was refused: %+v", findings)
	}
	unasked := strings.Replace(reversibleRunbook("undo.never", ""), "reversible: true\n", "", 1)
	if findings := validate.ReversibleRule(validate.WorldView{DAG: yamlDAG(t, unasked)}); len(findings) != 0 {
		t.Errorf("a runbook that is not reversible was refused: %+v", findings)
	}
}

// TestRollbackRule_HoldsStepsToWhatATaskIsHeldTo: an unknown method and an
// undeclared parameter in a rollback step are found at validation, with
// the step named, not at the rollback that needs them.
func TestRollbackRule_HoldsStepsToWhatATaskIsHeldTo(t *testing.T) {
	registerUndoShapes(t)
	runbook := `id: r
hosts: web
tasks:
  - name: step
    undo.never:
      name: x
    rollback:
      - undo.target:
          name: x
          colour: red
      - undo.missing:
          name: x
`
	findings := validate.RollbackRule(validate.WorldView{DAG: yamlDAG(t, runbook)})
	var messages []string
	for _, f := range findings {
		if f.RuleName != "rollback" {
			t.Errorf("finding %+v is not labeled as the rollback rule's", f)
		}
		messages = append(messages, f.Node+": "+f.Message)
	}
	joined := strings.Join(messages, "\n")
	for _, want := range []string{"tasks[0].rollback[0]", "colour", "tasks[0].rollback[1]", "undo.missing"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings do not mention %q:\n%s", want, joined)
		}
	}
	// The control: clean steps pass.
	clean := strings.Replace(strings.Replace(runbook, "          colour: red\n", "", 1), "      - undo.missing:\n          name: x\n", "", 1)
	if findings := validate.RollbackRule(validate.WorldView{DAG: yamlDAG(t, clean)}); len(findings) != 0 {
		t.Errorf("clean rollback steps were refused: %+v", findings)
	}
}
