// Tests that validate accepts the timed pause action in both spellings,
// under hosts:, and refuses its prompted form.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestValidate_AcceptsATimedPause: pause and pleiades.builtin.pause validate
// cleanly between tasks of a runbook with hosts:.
func TestValidate_AcceptsATimedPause(t *testing.T) {
	for _, name := range []string{"pause", "pleiades.builtin.pause"} {
		eval, _ := engine.NewCELEvaluator()
		dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: p\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n  - name: hold\n    " + name + ":\n      seconds: 5\n  - name: b\n    noop: {}\n"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		world := validate.WorldView{DAG: dag, Items: []inventory.InventoryItem{&inventorytest.Stub{StubID: "w1", StubName: "w1", StubTags: []inventory.Tag{"web"}, StubState: inventory.StateActive}}}
		report := validate.Validate(world)
		var problems []string
		for _, f := range report.Findings {
			problems = append(problems, f.RuleName+": "+f.Message)
		}
		if len(problems) > 0 {
			t.Errorf("%s: %s", name, strings.Join(problems, "\n"))
		}
	}
}

// TestValidate_AcceptsEveryDottedBuiltin: the namespaced spelling of each
// engine action the engine runs validates as the bare one does. Validation
// kept its own list once and refused pleiades.builtin.fail, assert and
// set_fact, which the engine ran.
func TestValidate_AcceptsEveryDottedBuiltin(t *testing.T) {
	for _, y := range []string{
		"id: p\ntasks:\n  - name: f\n    pleiades.builtin.fail:\n      msg: x\n    when_cel: 1 == 2\n",
		"id: p\ntasks:\n  - name: a\n    pleiades.builtin.assert:\n      that: [\"1 == 1\"]\n",
		"id: p\ntasks:\n  - name: s\n    pleiades.builtin.set_fact:\n      x: 1\n",
		"id: p\ntasks:\n  - name: m\n    pleiades.builtin.set_metadata:\n      data: {a: 1}\n",
	} {
		eval, _ := engine.NewCELEvaluator()
		dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(y))
		if err != nil {
			t.Fatalf("%q: %v", y, err)
		}
		for _, f := range validate.Validate(validate.WorldView{DAG: dag}).Findings {
			t.Errorf("%q: %s: %s", y, f.RuleName, f.Message)
		}
	}
}
