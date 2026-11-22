// Tests of the three-valued condition (facts_condition.go): a key with no
// answer is a CEL unknown, a result CEL decides without it stands, and a
// result left unknown fails the task naming the keys it depended on, however
// the condition tries to turn the unknown into an answer.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// threeValuedReader holds tfx.mem_mb on both devices and nothing else, so
// tfx.kernel and uptime have no answer.
func threeValuedReader() *fakeReader {
	mem := engine.FactAnswer{State: engine.FactPresent, Value: 4096}
	return &fakeReader{answers: map[string]map[string]engine.FactAnswer{"dev-1": {"tfx.mem_mb": mem}, "dev-2": {"tfx.mem_mb": mem}}}
}

// runThreeValued runs a gather of tfx.load alone (registered as g, narrowed
// so it says nothing of any other key) and then a probe under cond, with
// vars.on true and vars.off false, and returns the probe's results.
func runThreeValued(t *testing.T, cond string) (map[string]engine.NodeResult, *factsRead) {
	t.Helper()
	m := registerFactsRead(t)
	x := newCheckExecutor(factsFleet(), engine.WithFactReader(threeValuedReader()), engine.WithVariables(map[string]any{"on": true, "off": false}))
	result, err := x.Run(context.Background(), factsDAG(t, "id: three\nhosts: fleet\ntasks:\n  - name: gather\n    register: g\n    tfx.gather: {only: [tfx.load]}\n  - name: read\n    "+cond+"\n    tfx.probe: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	return resultsOf(result, "tasks[1]"), m
}

// TestFactsRoot_AnUnknownResultFailsTheTaskHoweverItIsTested: each way a
// condition could try to turn an unknown key into an answer leaves its result
// unknown, and the task fails naming the task, the key and the method that
// would gather it; the probe never runs.
func TestFactsRoot_AnUnknownResultFailsTheTaskHoweverItIsTested(t *testing.T) {
	for name, cond := range map[string]string{
		"a comparison":                 `when: facts["tfx.kernel"] == "6.1"`,
		"is defined":                   `when: facts["tfx.kernel"] is defined`,
		"is not defined":               `when: facts["tfx.kernel"] is not defined`,
		"in":                           `when_cel: '!("tfx.kernel" in facts)'`,
		"has":                          `when_cel: '!has(facts.uptime)'`,
		"an optional hasValue":         `when_cel: '!facts[?"tfx.kernel"].hasValue()'`,
		"an optional default":          `when_cel: 'facts[?"tfx.kernel"].orValue("none") == "none"'`,
		"an optional field default":    `when_cel: 'facts.?uptime.orValue(0) == 0'`,
		"a ternary":                    `when_cel: 'facts[?"tfx.kernel"].hasValue() ? true : true'`,
		"a nested ternary":             `when_cel: 'vars.on ? (facts["tfx.kernel"] == "6.1" ? true : false) : true'`,
		"type()":                       `when_cel: 'type(facts["tfx.kernel"]) == string'`,
		"a comprehension":              `when_cel: '[facts["tfx.kernel"]].exists(k, k == "6.1")'`,
		"a function":                   `when_cel: 'filters.safeInt(facts["tfx.kernel"], 0) == 0'`,
		"unknown && true":              `when_cel: 'facts["tfx.kernel"] == "6.1" && true'`,
		"a when list (AND)":            "when:\n      - \"true\"\n      - facts[\"tfx.kernel\"] is not defined",
		"a when_or with no true item":  "when_or:\n      - \"false\"\n      - facts[\"tfx.kernel\"] is not defined",
		"vars, result and facts mixed": `when_cel: 'vars.on && result.g["tfx.load"] > 0.5 && facts["tfx.kernel"] == "6.1"'`,
	} {
		t.Run(name, func(t *testing.T) {
			runs, m := runThreeValued(t, cond)
			if len(runs) != 2 {
				t.Fatalf("results %v, want one per device", runs)
			}
			for device, n := range runs {
				if n.Err == nil || n.Skipped {
					t.Fatalf("on %s: %s, want the task failed", device, describe(n))
				}
				msg := n.Err.Error()
				if !strings.Contains(msg, `tasks[1] (name "read")`) || !strings.Contains(msg, "cannot be decided without it") || !strings.Contains(msg, "the task did not run") {
					t.Errorf("on %s the failure says %q, want it to name the task and say nothing ran", device, msg)
				}
				if !strings.Contains(msg, `facts["tfx.kernel"]`) && !strings.Contains(msg, `facts["uptime"]`) {
					t.Errorf("on %s the failure says %q, want it to name the key", device, msg)
				}
				if strings.Contains(msg, `facts["tfx.mem_mb"]`) {
					t.Errorf("on %s the failure names tfx.mem_mb, which has an answer: %q", device, msg)
				}
			}
			if m.probeRuns.Load() != 0 {
				t.Errorf("the probe ran %d times on an unknown result", m.probeRuns.Load())
			}
		})
	}
}

// TestFactsRoot_AResultDecidedWithoutAnUnknownKeyStands: a condition CEL
// decides without the key that has no answer runs or skips as it says, and
// never fails for that key: true || unknown is true, false && unknown is
// false, a false item of a when list or a true item of a when_or decides the
// list, and a present key decides an || before an unknown one.
func TestFactsRoot_AResultDecidedWithoutAnUnknownKeyStands(t *testing.T) {
	for name, tc := range map[string]struct {
		cond string
		runs bool
	}{
		"true || unknown":                     {`when_cel: 'true || facts["tfx.kernel"] == "x"'`, true},
		"false && unknown":                    {`when_cel: 'false && facts["tfx.kernel"] == "x"'`, false},
		"unknown && false":                    {`when_cel: 'facts["tfx.kernel"] == "x" && false'`, false},
		"a present key decides an or":         {`when_cel: 'facts["tfx.mem_mb"] == 4096 || facts["tfx.kernel"] == "x"'`, true},
		"a false item of a when list":         {"when:\n      - vars.off\n      - facts[\"tfx.kernel\"] == \"1\"", false},
		"a true item of a when_or":            {"when_or:\n      - facts[\"tfx.kernel\"] == \"1\"\n      - vars.on", true},
		"vars, result and facts, decided":     {`when_cel: '!vars.on && result.g["tfx.load"] > 0.5 && facts["tfx.kernel"] == "6.1"'`, false},
		"a ternary that never reads the fact": {`when_cel: 'vars.on ? true : facts["tfx.kernel"] == "x"'`, true},
		// is defined stays exact beside an unknown key, where "k" in facts
		// would read the root whole and go unknown.
		"is defined beside an unknown key": {"when_or:\n      - facts[\"tfx.mem_mb\"] is defined\n      - facts[\"tfx.kernel\"] == \"x\"", true},
	} {
		t.Run(name, func(t *testing.T) {
			runs, m := runThreeValued(t, tc.cond)
			for device, n := range runs {
				if n.Err != nil || n.Skipped == tc.runs {
					t.Errorf("on %s: %s, want runs=%v and no failure", device, describe(n), tc.runs)
				}
			}
			if want := int32(0); tc.runs {
				want = 2
				if m.probeRuns.Load() != want {
					t.Errorf("the probe ran %d times, want %d", m.probeRuns.Load(), want)
				}
			} else if m.probeRuns.Load() != want {
				t.Errorf("the probe ran %d times, want none", m.probeRuns.Load())
			}
		})
	}
}

// TestFactsRoot_TheFailureNamesTheMethodsThatGather: a key a method declares
// by name names that method; an item of a family names the method that lists
// the family and says to test its result; a key nothing declares says so.
func TestFactsRoot_TheFailureNamesTheMethodsThatGather(t *testing.T) {
	for name, tc := range map[string]struct{ key, want string }{
		"a key by name":      {"tfx.kernel", "through tfx.gather"},
		"an item of family":  {"tfx.pkg.nginx", "tfx.install lists only where the device has it"},
		"a key none declare": {"acme.widget", "no method this binary knows declares it"},
	} {
		t.Run(name, func(t *testing.T) {
			runs, _ := runThreeValued(t, `when: facts["`+tc.key+`"] == "x"`)
			for device, n := range runs {
				if n.Err == nil || !strings.Contains(n.Err.Error(), tc.want) {
					t.Errorf("on %s: %s, want it to say %q", device, describe(n), tc.want)
				}
			}
		})
	}
}
