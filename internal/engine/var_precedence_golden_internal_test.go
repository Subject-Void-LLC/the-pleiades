// Package engine: the variable precedence order (vars.go's opening
// comment), pinned by a golden file.
//
// The order the seven tiers merge in is a contract every runbook relies on
// and no schema states. This test calls (*run).mergedVars, the one function
// that merges them, with each tier filled the way a run fills it: a block's
// and a task's vars: through propagateVars on a real WorkflowDef, the set_fact
// layers through the run's own factStore, the device's vars through the
// host-vars root, and the extra variables through WithVariables. It records
// which tier wins for each name in testdata/golden/var_precedence.json.
package engine

import (
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/golden"
)

// precedenceLevels are the tiers, widest first, as vars.go numbers them.
var precedenceLevels = []string{
	"runbook_vars", "global_set_fact", "device_vars", "device_set_fact", "block_vars", "task_vars", "extra_vars",
}

// precedenceSetup is one arrangement of tiers and the winner of each name.
type precedenceSetup struct {
	// Levels are the tiers present in this setup.
	Levels []string `json:"levels"`
	// Winners maps each name to the value mergedVars returned for it, which
	// is the name of the tier that set it.
	Winners map[string]any `json:"winners"`
}

// mergeAt fills the tiers named in present with values, each set for the
// names that tier carries in sets, and returns mergedVars' result.
func mergeAt(t *testing.T, sets map[string][]string) map[string]any {
	t.Helper()
	tier := func(level string) map[string]any {
		out := map[string]any{}
		for _, name := range sets[level] {
			out[name] = level
		}
		return out
	}
	// A block holding one task, so block vars: reach it as a run's would.
	def := WorkflowDef{Tasks: []Task{{Name: "outer", Vars: tier("block_vars"), Block: []Task{{Name: "inner", FQCN: "noop", Vars: tier("task_vars")}}}}}
	propagateVars(&def)
	task := def.Tasks[0].Block[0]

	r := &run{
		x:     NewExecutor(nil, nil, nil, nil, nil, 0, WithVariables(tier("extra_vars"))),
		dag:   &DAG{Vars: tier("runbook_vars")},
		facts: newFactStore(),
	}
	if f := tier("global_set_fact"); len(f) > 0 {
		if err := r.facts.setGlobal("tasks[0]", "dev-1", f, false); err != nil {
			t.Fatal(err)
		}
	}
	if f := tier("device_set_fact"); len(f) > 0 {
		r.facts.set("dev-1", f, false)
	}
	roots := map[string]any{
		TemplateRootDevice: map[string]any{"id": "dev-1"},
		hostVarsRoot:       tier("device_vars"),
	}
	got, err := r.mergedVars(&task, roots)
	if err != nil {
		t.Fatalf("mergedVars: %v", err)
	}
	return got
}

// TestVariablePrecedenceMatchesTheGolden records, for each setup holding
// the first k tiers, the winner of a name every present tier sets and of a
// name each pair of adjacent present tiers sets.
func TestVariablePrecedenceMatchesTheGolden(t *testing.T) {
	var setups []precedenceSetup
	for k := 1; k <= len(precedenceLevels); k++ {
		present := precedenceLevels[:k]
		sets := map[string][]string{}
		for i, level := range present {
			sets[level] = append(sets[level], "every_level")
			// The pair name for this tier and the one just below it.
			if i > 0 {
				pair := present[i-1] + "__" + level
				sets[present[i-1]] = append(sets[present[i-1]], pair)
				sets[level] = append(sets[level], pair)
			}
		}
		setups = append(setups, precedenceSetup{Levels: present, Winners: mergeAt(t, sets)})
	}
	golden.JSON(t, filepath.Join("testdata", "golden", "var_precedence.json"), struct {
		Order  []string          `json:"order"`
		Setups []precedenceSetup `json:"setups"`
	}{precedenceLevels, setups})
}
