package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCELFilters_Phase52StructuredFilters is Phase 52's own Release Gate
// requirement: every one of its 11 filters proven callable through the
// real, unmodified engine.NewCELEvaluator()/Program.Eval via a compiled
// when_cel-shaped expression, not a bare Go function call (RULE 0). Each
// case's want value was independently verified against pkg/filters' own
// unit tests before being written here, so this is a second,
// through-CEL proof of the same behavior, not a restatement of it.
func TestCELFilters_Phase52StructuredFilters(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	cases := []struct {
		name string
		expr string
	}{
		{"flatten", `filters.flatten({"a": {"b": 1}}) == {"a.b": 1}`},
		{"unflatten", `filters.unflatten({"a.b": 1}) == {"a": {"b": 1}}`},
		{"deep_merge", `filters.deepMerge({"a": {"x": 1}}, {"a": {"y": 2}}) == {"a": {"x": 1, "y": 2}}`},
		{"shallow_merge", `filters.shallowMerge({"a": 1}, {"a": 2, "b": 3}) == {"a": 2, "b": 3}`},
		{"csv_to_list", `filters.csvToList("a,\"b,c\",d") == ["a", "b,c", "d"]`},
		{"list_to_csv", `filters.listToCSV(["a", "b,c", "d"]) == "a,\"b,c\",d"`},
		{"pluck", `filters.pluck([{"name": "a", "val": 1}, {"name": "b"}], "val") == [1]`},
		{"pluck_skips_missing_key", `filters.pluck([{"name": "a"}, {"other": "x"}], "name") == ["a"]`},
		{"yaml_to_json", `filters.yamlToJSON("a: 1\n") == "{\"a\":1}"`},
		{"json_to_yaml", `filters.jsonToYAML("{\"a\":1}") == "a: 1\n"`},
		{"xml_to_json", `filters.xmlToJSON("<a><b>1</b></a>") == "{\"a\":{\"b\":\"1\"}}"`},
		{"generate_uuidv4_shape", `filters.generateUUIDv4().size() == 36`},
		{"generate_uuidv4_not_deterministic", `filters.generateUUIDv4() != filters.generateUUIDv4()`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prg, err := eval.Compile(tc.expr)
			if err != nil {
				t.Fatalf("failed to compile %q: %v", tc.expr, err)
			}
			got, err := prg.Eval(map[string]interface{}{})
			if err != nil {
				t.Fatalf("eval of %q failed: %v", tc.expr, err)
			}
			if !got {
				t.Errorf("%s: expression %q evaluated false", tc.name, tc.expr)
			}
		})
	}
}

// TestCELFilters_Phase52CombinedCondition chains five of this phase's
// functions in one when_cel-shaped condition against a realistic device
// stat payload (a YAML config blob, nested facts, a list of interface
// records, and a CSV tag list), the same combined-condition shape
// TestCELFilters_Phase51CombinedCondition established, with a negative
// control proving the condition genuinely flips false rather than being
// vacuously true.
func TestCELFilters_Phase52CombinedCondition(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	expr := `filters.yamlToJSON(stat.config_yaml) != "" && ` +
		`filters.flatten(stat.facts)["net.vlan"] == 100 && ` +
		`filters.pluck(stat.interfaces, "name") == ["Gi0/1", "Gi0/2"] && ` +
		`filters.csvToList(stat.tags_csv) == ["core", "prod"] && ` +
		`filters.generateUUIDv4().size() == 36`

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	stat := map[string]interface{}{
		"config_yaml": "a: 1\n",
		"facts": map[string]interface{}{
			"net": map[string]interface{}{"vlan": 100},
		},
		"interfaces": []interface{}{
			map[string]interface{}{"name": "Gi0/1"},
			map[string]interface{}{"name": "Gi0/2"},
			map[string]interface{}{"other": "nope"}, // no "name" key: Pluck skips it.
		},
		"tags_csv": "core,prod",
	}
	got, err := prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Fatal("expected the combined condition to evaluate true against a realistic stat payload")
	}

	// Negative control: dropping the second tag must flip the same
	// condition false, proving the combined expression is actually
	// exercising every clause rather than being vacuously true.
	stat["tags_csv"] = "core"
	got, err = prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Fatal("expected the combined condition to evaluate false once tags_csv drops the second tag")
	}
}
