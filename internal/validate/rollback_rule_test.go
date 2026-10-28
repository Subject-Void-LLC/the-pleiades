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
			Doc: collection.Doc{Params: []collection.Param{{Name: "name", Type: "string"}, {Name: "content", Type: "string"}}},
		}, Invoke: run}
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	// A method whose undo depends on the call: with keep: false it records
	// none (net.ios.config's checkpoint: false).
	byCall := collection.Descriptor{Name: "undo.bycall", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{target}},
		Doc: collection.Doc{Params: []collection.Param{{Name: "name", Type: "string"}, {Name: "keep", Type: "bool"}}},
	}, Invoke: run, UndoCall: func(params map[string]any) string {
		if v, ok := params["keep"]; ok && v != true {
			return "keep is not true"
		}
		return ""
	}}
	if err := collection.Register(byCall); err != nil {
		t.Fatal(err)
	}
	// A method whose undo may be partial, and says for which calls: with
	// keys: true it leaves something in place (net.ios.config's crypto key
	// lines).
	partialByCall := collection.Descriptor{Name: "undo.partialbycall", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Reversible: true,
			Inverses: []sdk.InverseSpec{{FQCN: "undo.target", Record: []string{"name"}, MayBePartial: true}}},
		Doc: collection.Doc{Params: []collection.Param{{Name: "name", Type: "string"}, {Name: "keys", Type: "bool"}}},
	}, Invoke: run, UndoCall: func(params map[string]any) string {
		if params["keys"] == true {
			return "keys is true, so the undo leaves the keys in place"
		}
		return ""
	}}
	if err := collection.Register(partialByCall); err != nil {
		t.Fatal(err)
	}
	// An external program's method, whose declaration is never honored.
	external := collection.Descriptor{Name: "undo.external", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{target}},
		Doc: collection.Doc{Params: []collection.Param{{Name: "name", Type: "string"}}},
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

// TestReversibleRule_AsksTheCallWhenTheMethodSaysItDepends: a method whose
// undo depends on the call is refused for the call that records none, by
// both rules that promise an undo, and passed for the one that records it.
func TestReversibleRule_AsksTheCallWhenTheMethodSaysItDepends(t *testing.T) {
	registerUndoShapes(t)
	if findings := validate.ReversibleRule(validate.WorldView{DAG: yamlDAG(t, reversibleRunbook("undo.bycall", ""))}); len(findings) != 0 {
		t.Errorf("a call that records its undo was refused: %+v", findings)
	}
	off := reversibleRunbook("undo.bycall", "      keep: false\n")
	if findings := validate.ReversibleRule(validate.WorldView{DAG: yamlDAG(t, off)}); len(findings) != 1 || !strings.Contains(findings[0].Message, "records no undo here: keep is not true") {
		t.Errorf("reversible: findings %+v, want the call refused naming why", findings)
	}
	block := "id: r\nhosts: web\ntasks:\n  - name: b\n    rollback_on_failure: true\n    block:\n      - name: step\n        undo.bycall:\n          name: x\n          keep: false\n"
	if findings := validate.RollbackOnFailureRule(validate.WorldView{DAG: yamlDAG(t, block)}); len(findings) != 1 || !strings.Contains(findings[0].Message, "keep is not true") {
		t.Errorf("rollback_on_failure: findings %+v, want the call refused", findings)
	}
}

// TestRollbackOnFailureRule_APartialUndoIsAskedOfTheCall: a method whose
// undo may be partial and that says for which calls (an UndoCall) is
// refused only for those calls, by both rules that promise an undo; one
// that does not say is still refused for every call. The real
// net.ios.config, whose undo is partial only for a crypto key line, passes
// a block of ordinary lines (the generated skill teaches exactly that) and
// is refused for a key line.
func TestRollbackOnFailureRule_APartialUndoIsAskedOfTheCall(t *testing.T) {
	registerUndoShapes(t)
	block := func(method, params string) string {
		return "id: r\nhosts: web\ntasks:\n  - name: b\n    rollback_on_failure: true\n    block:\n      - name: step\n        " + method + ":\n" + params
	}
	for name, c := range map[string]struct{ runbook, want string }{
		"a call the method says is exact":   {block("undo.partialbycall", "          name: x\n"), ""},
		"a call the method says is partial": {block("undo.partialbycall", "          name: x\n          keys: true\n"), "keys is true"},
		"a method that never says which":    {block("undo.partial", "          name: x\n"), "can leave part of what it changed in place"},
		"net.ios.config's ordinary lines":   {block("net.ios.config", "          lines:\n            - interface Loopback0\n            - description managed by pleiades\n"), ""},
		"net.ios.config's crypto key line":  {block("net.ios.config", "          lines:\n            - crypto key generate rsa modulus 2048\n"), "crypto key line"},
	} {
		t.Run(name, func(t *testing.T) {
			dag := yamlDAG(t, c.runbook)
			findings := validate.RollbackOnFailureRule(validate.WorldView{DAG: dag})
			reversible := strings.Replace(c.runbook, "tasks:\n", "reversible: true\ntasks:\n", 1)
			findings = append(findings, validate.ReversibleRule(validate.WorldView{DAG: yamlDAG(t, reversible)})...)
			switch {
			case c.want == "" && len(findings) != 0:
				t.Errorf("refused: %+v", findings)
			case c.want != "" && (len(findings) != 2 || !strings.Contains(findings[0].Message, c.want) || !strings.Contains(findings[1].Message, c.want)):
				t.Errorf("findings %+v, want both rules to refuse it naming %q", findings, c.want)
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

// TestRollbackOnFailureRule_RefusesATaskTheBlockCouldNotUndo: a block with
// rollback_on_failure is held to undoing exactly every task of its own,
// an inner block's rescue included; a task with a rollback: list, a
// read-only one, and anything outside the block's own tasks pass.
func TestRollbackOnFailureRule_RefusesATaskTheBlockCouldNotUndo(t *testing.T) {
	registerUndoShapes(t)
	runbook := `id: r
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: whole
        undo.whole: {name: a}
      - name: reads
        undo.reads: {name: a}
      - name: authored
        undo.never: {name: a}
        rollback:
          - undo.target: {name: a}
      - name: never
        undo.never: {name: a}
      - name: inner
        block:
          - name: inner ok
            undo.whole: {name: a}
        rescue:
          - name: inner rescue
            undo.withholding: {name: a}
    rescue:
      - name: outer rescue
        undo.never: {name: a}
  - name: after
    undo.never: {name: a}
`
	findings := validate.RollbackOnFailureRule(validate.WorldView{DAG: yamlDAG(t, runbook)})
	got := map[string]string{}
	for _, f := range findings {
		got[f.Node] = f.Message
	}
	if len(got) != 2 || !strings.Contains(got["tasks[0].block[3]"], "cannot be undone") || !strings.Contains(got["tasks[0].block[4].rescue[0]"], "keeps content out of the journal") {
		t.Errorf("findings %+v, want the undo.never task and the inner rescue's withholding task", findings)
	}
	for _, f := range findings {
		if f.RuleName != "rollback_on_failure" || !strings.Contains(f.Message, "remove rollback_on_failure") {
			t.Errorf("finding %+v does not name the rule and the way out", f)
		}
	}
	unasked := strings.Replace(runbook, "    rollback_on_failure: true\n", "", 1)
	if findings := validate.RollbackOnFailureRule(validate.WorldView{DAG: yamlDAG(t, unasked)}); len(findings) != 0 {
		t.Errorf("a block without the key was refused: %+v", findings)
	}
}
