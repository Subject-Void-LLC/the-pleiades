// Tests that a tag selection keeps what the runbook says about itself.
package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestSelect_KeepsReversibleAndHandlers: a DAG projected through a tag
// filter keeps the runbook's reversible: flag, which validation's
// ReversibleRule reads (Select dropped it, so --tags quietly turned the rule
// off), and the scopes and guards its blocks' rescue and always run by.
func TestSelect_KeepsReversibleAndHandlers(t *testing.T) {
	dag := yamlRunbook(t, `id: s
hosts: web
reversible: true
tasks:
  - name: guarded
    tags: [deploy]
    block:
      - name: b
        noop: {}
    always:
      - name: a
        noop: {}
  - name: other
    tags: [other]
    noop: {}
`)
	out, err := engine.Select(dag, engine.TagFilter{Tags: []string{"deploy"}})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !out.Reversible {
		t.Error("the selection dropped reversible: true")
	}
	if g := out.Guards["tasks[0]"]; !g.HasAlways() || len(g.Tasks) != 1 {
		t.Errorf("the selection's guard: %+v", g)
	}
	if _, kept := out.Nodes["tasks[1]"]; kept {
		t.Error("the filter kept a task it should leave out")
	}
}
