// Tests for HandlerWarnings through the real YAML builder, so the node IDs
// it reads are the ones a run gives.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// buildYAML builds a runbook through the real Builder.
func buildYAML(t *testing.T, src string) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(src))
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	return dag
}

// TestHandlerWarnings_NamesEachListThatWillNotRun: one warning per block and
// list, rescue before always, each counting its tasks once, a block nested in
// an always: counted under that always:, and none for a runbook without
// either.
func TestHandlerWarnings_NamesEachListThatWillNotRun(t *testing.T) {
	dag := buildYAML(t, `id: h
tasks:
  - name: first
    noop: {}
  - name: guarded
    block:
      - name: work
        noop: {}
    rescue:
      - name: note it
        noop: {}
    always:
      - name: clean up
        noop: {}
      - name: nested
        block:
          - name: inner
            noop: {}
        rescue:
          - name: inner rescue
            noop: {}
`)
	got := validate.HandlerWarnings(dag)
	if len(got) != 2 {
		t.Fatalf("warnings %v, want one for the rescue and one for the always", got)
	}
	for i, want := range []struct{ kind, count string }{{"rescue", "holds 1 task(s)"}, {"always", "holds 3 task(s)"}} {
		w := got[i]
		if w.RuleName != validate.HandlerRule || w.Node != "tasks[1]" || !strings.Contains(w.Message, "tasks[1]'s "+want.kind+":") ||
			!strings.Contains(w.Message, want.count) || !strings.Contains(w.Message, "does not run "+want.kind+": yet") {
			t.Errorf("warning %d: %+v", i, w)
		}
	}
	if !strings.Contains(got[1].Message, "while the run still ends complete") {
		t.Errorf("the always warning does not say the run still ends complete: %s", got[1].Message)
	}

	// Two blocks are named in node order, each with its own list.
	two := validate.HandlerWarnings(buildYAML(t, `id: two
tasks:
  - name: b
    block:
      - name: x
        noop: {}
    always:
      - name: y
        noop: {}
  - name: a
    block:
      - name: x
        noop: {}
    rescue:
      - name: y
        noop: {}
`))
	if len(two) != 2 || two[0].Node != "tasks[0]" || two[1].Node != "tasks[1]" {
		t.Errorf("two blocks: %v", two)
	}

	if w := validate.HandlerWarnings(buildYAML(t, "id: n\ntasks:\n  - name: one\n    noop: {}\n")); len(w) != 0 {
		t.Errorf("a runbook with no rescue or always warned: %v", w)
	}
	if w := validate.HandlerWarnings(nil); w != nil {
		t.Errorf("no DAG warned: %v", w)
	}
}
