// Tests of where the facts root may be read: a condition by a literal key,
// and nowhere else, each refusal made when the runbook is built (or, for a
// parameter, when the task renders) and saying what is allowed.
package engine_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"cel.dev/cel-go/cel"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestFactsRoot_ARefusedReadIsRefusedWhenTheRunbookIsBuilt: the whole root
// used as a value, a key computed at run time, another device's facts, a
// name no fact can have, and the root in until, changed_when, failed_when,
// an assert's that or a loop label each fail the build, naming the task and
// what is allowed.
func TestFactsRoot_ARefusedReadIsRefusedWhenTheRunbookIsBuilt(t *testing.T) {
	registerFactsRead(t)
	const allowed = `facts["os.family"]`
	for name, tc := range map[string]struct{ task, want string }{
		"the root counted":         {`when_cel: 'size(facts) > 0'`, allowed},
		"the root compared":        {`when_cel: 'facts == {}'`, allowed},
		"the root iterated":        {`when_cel: 'facts.exists(k, k == "x")'`, allowed},
		"the root to a function":   {`when_cel: 'json.encode(facts) != ""'`, allowed},
		"the root in a list":       {`when_cel: 'facts in [facts]'`, allowed},
		"a computed key":           {`when: facts[vars.key] == "x"`, "computed while the run goes"},
		"a computed presence test": {`when_cel: 'vars.key in facts'`, "computed while the run goes"},
		"a computed optional key":  {`when_cel: 'facts[?vars.key].hasValue()'`, "computed while the run goes"},
		"another device's":         {`when_cel: 'hostvars["web2"]["os.family"] == "debian"'`, "another device's facts"},
		"not a fact name":          {`when: facts["a b"] == "x"`, "not a fact name"},
		"in until":                 {"register: r\n    until: facts[\"tfx.kernel\"] == \"6.1\"\n    retries: 1\n    delay: 0", "its until reads facts"},
		"in changed_when":          {"register: r\n    changed_when: facts[\"tfx.kernel\"] == \"6.1\"", "its changed_when reads facts"},
		"in failed_when":           {"register: r\n    failed_when: facts[\"tfx.kernel\"] is defined", "its failed_when reads facts"},
		"in a loop label":          {"loop: [1]\n    loop_control:\n      label: \"{{ facts['tfx.kernel'] }}\"", "loop_control label"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := factsDAGErr("id: refused\nhosts: fleet\ntasks:\n  - name: t\n    " + tc.task + "\n    tfx.probe: {}\n")
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `tasks[0] (name "t")`) {
				t.Fatalf("build error = %v, want it to name the task and say %q", err, tc.want)
			}
		})
	}
	t.Run("in an assert's that", func(t *testing.T) {
		_, err := factsDAGErr("id: refused\nhosts: fleet\ntasks:\n  - name: t\n    assert:\n      that:\n        - facts[\"tfx.kernel\"] == \"6.1\"\n")
		if err == nil || !strings.Contains(err.Error(), "its that reads facts") {
			t.Fatalf("build error = %v, want the that refused", err)
		}
	})
	t.Run("a rollback step's condition", func(t *testing.T) {
		_, err := factsDAGErr("id: refused\nhosts: fleet\ntasks:\n  - name: t\n    tfx.probe: {}\n    rollback:\n      - tfx.probe: {}\n        when: facts[\"tfx.kernel\"] == \"6.1\"\n")
		if err == nil || !strings.Contains(err.Error(), "sets when") {
			t.Fatalf("build error = %v, want a rollback step's condition refused", err)
		}
	})
}

// TestFactsRoot_EveryAllowedSpellingReadsItsLiteralKey: each way a condition
// may read a fact builds, and the key it reads is the one it names.
func TestFactsRoot_EveryAllowedSpellingReadsItsLiteralKey(t *testing.T) {
	for cond, want := range map[string][]string{
		`when: facts["os.family"] == "debian"`:                {"os.family"},
		`when: facts['os.family'] is defined`:                 {"os.family"},
		`when_cel: 'has(facts.uptime)'`:                       {"uptime"},
		`when_cel: '"os.family" in facts'`:                    {"os.family"},
		`when_cel: 'facts[?"os.family"].orValue("") == "x"'`:  {"os.family"},
		`when_cel: 'facts.?uptime.hasValue()'`:                {"uptime"},
		`when_cel: 'facts["a.b"] == "x" || facts["c.d"] > 1'`: {"a.b", "c.d"},
		`when_cel: 'facts["os.family"].startsWith("deb")'`:    {"os.family"},
		`when_cel: '"facts" == vars.word'`:                    nil,
		// A comprehension's own variable named facts is that variable, not
		// the root; the root read beside it is still a read.
		`when_cel: '["debian"].exists(facts, facts == "debian")'`:                         nil,
		`when_cel: '["a"].exists(facts, facts == "a") && facts["os.family"] == "debian"'`: {"os.family"},
	} {
		dag, err := factsDAGErr("id: ok\nhosts: fleet\ntasks:\n  - name: t\n    " + cond + "\n    noop: {}\n")
		if err != nil {
			t.Errorf("%s: %v", cond, err)
			continue
		}
		var got []string
		for _, r := range engine.FactReadsOf(dag) {
			got = append(got, r.Keys...)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s reads %v, want %v", cond, got, want)
		}
	}
}

// TestFactsRoot_ABuildWithoutTheRootRefusesARunbookReadingIt: a build from
// before the facts root (a Runner of an earlier release) declares no facts
// variable, and CEL's checker refuses an undeclared name, so such a build
// fails to compile every spelling of a fact read rather than reading it as
// missing: it refuses the dispatch when it resolves the runbook, before any
// task, and no payload version is needed to make it fail closed. The
// variables are the earlier release's, written out here.
func TestFactsRoot_ABuildWithoutTheRootRefusesARunbookReadingIt(t *testing.T) {
	var before []cel.EnvOption
	for _, name := range []string{"stat", "nodes", "vars", "result", "error", "device", "loop"} {
		before = append(before, cel.Variable(name, cel.MapType(cel.StringType, cel.DynType)))
	}
	before = append(append(before, cel.Variable("item", cel.DynType)), engine.CELLibraryOptions()...)
	env, err := cel.NewEnv(before...)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{`facts["os.version"] == "17.3"`, `has(facts.uptime)`, `"os.version" in facts`, `facts[?"os.version"].hasValue()`, `!("os.version" in facts)`} {
		if _, iss := env.Compile(src); iss == nil || iss.Err() == nil || !strings.Contains(iss.Err().Error(), "undeclared reference to 'facts'") {
			t.Errorf("%s compiled in a build without the root (%v)", src, iss.Err())
		}
	}
}

// TestFactsRoot_NoParameterReadsAFact: a parameter and a set_fact's value
// reading facts fail when the task renders, so no fact's value reaches a
// method or the run's variables (validation refuses them, and a rollback
// step's, before the run); the refusal says where a fact may be read.
func TestFactsRoot_NoParameterReadsAFact(t *testing.T) {
	m := registerFactsRead(t)
	for name, runbook := range map[string]string{
		"a parameter": "  - name: t\n    tfx.install: {name: \"{{ facts['tfx.kernel'] }}\"}\n",
		"a set_fact":  "  - name: t\n    set_fact: {kernel: \"{{ facts['tfx.kernel'] }}\"}\n  - name: u\n    when: vars.kernel == \"6.1\"\n    tfx.probe: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithRenderer(render.New())).Run(context.Background(),
				factsDAG(t, "id: params\nhosts: fleet\ntasks:\n  - name: gather\n    tfx.gather: {}\n"+runbook))
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for _, n := range result.Nodes {
				if n.NodeID == "tasks[1]" && n.Err != nil && strings.Contains(n.Err.Error(), "a parameter may not") && strings.Contains(n.Err.Error(), "when, when_or or when_cel") {
					failed = true
				}
			}
			if !failed {
				t.Fatalf("results %+v, want the parameter refused naming where a fact may be read", result.Nodes)
			}
			if m.probeRuns.Load() != 0 {
				t.Error("a task after the refused set_fact ran")
			}
		})
	}
}

// TestConditions_OneThatIsNotTrueOrFalseIsRefusedWhenBuilt: a condition the
// checker knows to be a string, a number or an optional (facts[?"key"]
// alone) is refused when it compiles, naming its type; one of unknown type,
// such as a register's field, is left to the run.
func TestConditions_OneThatIsNotTrueOrFalseIsRefusedWhenBuilt(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{`facts[?"os.codename"]`, `"yes"`, `1 + 1`} {
		if _, err := eval.Compile(src); err == nil || !strings.Contains(err.Error(), "not true or false") {
			t.Errorf("%s: %v, want it refused as not true or false", src, err)
		}
	}
	for _, src := range []string{`true`, `stat.flag`, `facts[?"os.codename"].hasValue()`} {
		if _, err := eval.Compile(src); err != nil {
			t.Errorf("%s: %v, want it compiled", src, err)
		}
	}
}
