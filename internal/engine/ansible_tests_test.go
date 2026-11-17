// Tests of Ansible's is defined in a when: item, through the real builder
// and Executor: it decides as has() does, and is refused on anything but a
// variable reference.
package engine_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// definedRunbook gates one rbtest.make call on each spelling of the test,
// Ansible's and CEL's, over vars.region.
const definedRunbook = `id: defined
hosts: web
tasks:
  - name: is defined
    when: vars.region is defined
    rbtest.make:
      name: r
      note: defined
  - name: is not defined
    when: vars.region is not defined
    rbtest.make:
      name: r
      note: not-defined
  - name: is undefined
    when:
      - vars.region is undefined
    rbtest.make:
      name: r
      note: undefined
  - name: is not undefined
    when_or:
      - vars.region   is   not   undefined
    rbtest.make:
      name: r
      note: not-undefined
  - name: has
    when: has(vars.region)
    rbtest.make:
      name: r
      note: has
  - name: not has
    when: "!has(vars.region)"
    rbtest.make:
      name: r
      note: not-has
`

// madeNames returns the note each rbtest.make call was given, from the
// "<device>: <note>" lines paramsSeen keeps, for device a alone.
func madeNames(seen []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range seen {
		if name, ok := strings.CutPrefix(s, "a: "); ok {
			out[name] = true
		}
	}
	return out
}

// TestWhen_IsDefinedDecidesAsHasDoes: with vars.region given, the tasks
// testing it defined run and those testing it absent are skipped, exactly
// as has() and !has() decide; with it not given, the other way round.
func TestWhen_IsDefinedDecidesAsHasDoes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		want  []string
	}{
		{"given", map[string]any{"region": "us-east-1"}, []string{"defined", "not-undefined", "has"}},
		{"not given", map[string]any{"other": "x"}, []string{"not-defined", "undefined", "not-has"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen, result := runVars(t, varsDevices(nil, nil), tc.extra, definedRunbook)
			if result.HasErrors() {
				t.Fatalf("%+v", result.Nodes)
			}
			made := madeNames(seen)
			if len(made) != len(tc.want) {
				t.Errorf("ran %v, want %v", seen, tc.want)
			}
			for _, w := range tc.want {
				if !made[w] {
					t.Errorf("%s did not run; ran %v", w, seen)
				}
			}
		})
	}
}

// TestWhen_IsDefinedOnlyOfAVariableReference: a definedness test of a
// deeper path, a literal or a bare name is refused when the runbook is
// built, naming the form that works, in when: and in failed_when:; in when_cel,
// which is CEL as written, it is CEL's own parse error.
func TestWhen_IsDefinedOnlyOfAVariableReference(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	build := func(key, expr string) error {
		_, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: refused\nhosts: web\ntasks:\n  - name: t\n    " + key + ": \"" + expr + "\"\n    register: out\n    noop: {}\n"))
		return err
	}
	for _, expr := range []string{"vars.a.b is defined", "'x' is defined", "region is defined", "vars['a'] is not defined", "size(vars) is undefined"} {
		err := build("when", expr)
		if err == nil || !strings.Contains(err.Error(), "asked only of a variable reference") {
			t.Errorf("when: %s: err = %v, want the variable-reference refusal", expr, err)
		}
	}
	if err := build("failed_when", "vars.a.b is defined"); err == nil || !strings.Contains(err.Error(), "asked only of a variable reference") {
		t.Errorf("failed_when: err = %v, want the variable-reference refusal", err)
	}
	if err := build("when_cel", "vars.a is defined"); err == nil || strings.Contains(err.Error(), "asked only of a variable reference") {
		t.Errorf("when_cel: err = %v, want CEL's own parse error", err)
	}
	// A string that merely holds the words is CEL, and compiles as it is.
	if err := build("when", "vars.msg == 'it is defined'"); err != nil {
		t.Errorf("a comparison with the words in a string was refused: %v", err)
	}
}

// TestWhen_IsDefinedSkipQuotesTheItemAsWritten: a skip names the
// author's own item, not the has() it compiled to.
func TestWhen_IsDefinedSkipQuotesTheItemAsWritten(t *testing.T) {
	_, result := runVars(t, varsDevices(nil, nil), map[string]any{"other": "x"}, `id: reason
hosts: web
tasks:
  - name: gated
    when: vars.region is defined
    rbtest.make:
      name: r
      note: gated
`)
	for _, n := range result.Nodes {
		if n.Skipped && !strings.Contains(n.SkipReason, "`vars.region is defined`") {
			t.Errorf("skip reason %q does not quote the item as written", n.SkipReason)
		}
	}
}

// TestConditionSources_ReadIsDefinedAsHas: the readers that parse a
// condition (which registers it reads, which it tests for) read an Ansible
// is defined as the has() it compiles to.
func TestConditionSources_ReadIsDefinedAsHas(t *testing.T) {
	c := engine.Conditional{When: engine.StringList{"result.probe is defined", "result.other is not defined", "", "vars.x.y is defined"}}
	got := c.Sources()
	want := []string{"has(result.probe)", "!has(result.other)", "vars.x.y is defined"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Sources = %q, want %q", got, want)
	}
	tested, err := engine.ConditionTestsPresence(engine.Conditional{When: engine.StringList{"result.probe is defined"}})
	if err != nil || !tested["probe"] {
		t.Errorf("ConditionTestsPresence = %v, %v, want probe tested", tested, err)
	}
	if got := (engine.Conditional{WhenCEL: "has(vars.a)"}).Sources(); len(got) != 1 || got[0] != "has(vars.a)" {
		t.Errorf("when_cel Sources = %q", got)
	}
}
