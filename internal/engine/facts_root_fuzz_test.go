// Fuzz of the facts root's detection (facts_root.go) against its evaluation
// (facts_view.go): whatever a condition is, detection never panics, every key
// it accepts is a fact name, and an evaluation of a condition it accepts
// reads no key it did not name and never the root as a whole, so a run reads
// exactly the facts its plan says it does.
package engine

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// everyKeyPresent answers every key it is asked as present.
type everyKeyPresent struct{}

func (everyKeyPresent) ReadFacts(_ context.Context, _ string, keys []string) (map[string]FactAnswer, error) {
	out := make(map[string]FactAnswer, len(keys))
	for _, k := range keys {
		out[k] = FactAnswer{State: FactPresent, Value: map[string]any{"version": "1"}}
	}
	return out, nil
}

// fuzzEvaluator is one evaluator for every fuzz input, as a run shares one.
var fuzzEvaluator = sync.OnceValues(NewCELEvaluator)

func FuzzConditionFactReads(f *testing.F) {
	for _, seed := range []string{
		`facts["os.family"] == "debian"`, `has(facts.uptime)`, `"a.b" in facts`, `facts[?"a.b"].orValue(1) == 1`,
		`facts.?uptime.hasValue()`, `size(facts) > 0`, `facts[vars.k] == 1`, `vars.k in facts`, `facts == {}`,
		`[1].exists(facts, facts > 0)`, `facts["a"]["b"] == 1`, `dyn(facts)["a"] == 1`, `hostvars["x"] == 1`,
		`facts["a b"] == 1`, `true || facts["a.b"] == 1`, `{"facts": 1}.facts == 1`, `facts.a.b == "x"`,
		`facts[1] == 2`, `facts.exists(k, k == "a")`, `"facts" == "facts"`, `facts`, `facts[`, `facts[?vars.k]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		reads, err := factReadsOf([]string{src})
		if err != nil {
			// Refused: a condition that does not parse reads nothing here
			// and fails its own compile.
			return
		}
		for _, k := range reads.Keys {
			if !facts.ReadableKey(k) {
				t.Fatalf("%q: detection accepted %q, which is not a fact name", src, k)
			}
		}
		if len(reads.Problems) > 0 {
			return
		}
		eval, err := fuzzEvaluator()
		if err != nil {
			t.Fatal(err)
		}
		prg, err := eval.Compile(src)
		if err != nil {
			return
		}
		named := map[string]bool{}
		for _, k := range reads.Keys {
			named[k] = true
		}
		x := NewExecutor(nil, nil, nil, nil, nil, 0, WithFactReader(everyKeyPresent{}))
		r := &run{x: x, secrets: newStringSet(), knowledge: &factKnowledge{keys: reads.Keys, devices: map[string]*deviceFacts{}, missed: map[string]map[string]bool{}}}
		view := &factsView{ctx: context.Background(), r: r, device: "dev-1", named: named}
		vars := map[string]any{TemplateRootFacts: view, "vars": map[string]any{"k": "a.b"}, "stat": map[string]any{}, "nodes": map[string]any{},
			"result": map[string]any{}, "device": map[string]any{}, "loop": map[string]any{}, "error": map[string]any{}, "item": 1}
		_, _ = prg.Eval(vars)
		if view.whole {
			t.Fatalf("%q: detection found no refused read, and the evaluation used the facts root as a whole", src)
		}
		for _, m := range view.misses {
			if m.err != nil && strings.Contains(m.err.Error(), "does not name it") {
				t.Fatalf("%q: the evaluation read %q, which detection did not name (it named %v)", src, m.key, reads.Keys)
			}
		}
	})
}
