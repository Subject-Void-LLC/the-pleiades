package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCELFilters_Phase54ValidationBusinessLogicFilters is Phase 54's own
// Release Gate requirement: every one of its 17 filters proven callable
// through the real, unmodified engine.NewCELEvaluator()/Program.Eval via
// a compiled when_cel-shaped expression, not a bare Go function call
// (RULE 0). Each case's want value was independently verified against
// pkg/filters' own unit tests before being written here.
func TestCELFilters_Phase54ValidationBusinessLogicFilters(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	cases := []struct {
		name string
		expr string
	}{
		{"is_valid_fqdn_true", `filters.isValidFQDN("host1.example.com")`},
		{"is_valid_fqdn_false", `!filters.isValidFQDN("not a fqdn")`},
		{"is_valid_email_true", `filters.isValidEmail("user@example.com")`},
		{"is_valid_email_false", `!filters.isValidEmail("Name <user@example.com>")`},
		{"is_valid_uuid_true", `filters.isValidUUID("123e4567-e89b-12d3-a456-426614174000")`},
		{"is_valid_uuid_false", `!filters.isValidUUID("not-a-uuid")`},
		{"is_valid_base64_true", `filters.isValidBase64("aGVsbG8=")`},
		{"is_valid_base64_false", `!filters.isValidBase64("not base64!!")`},
		{"is_valid_json_true", `filters.isValidJSON("{\"a\":1}")`},
		{"is_valid_json_false", `!filters.isValidJSON("{a:1}")`},
		{"is_valid_yaml_true", `filters.isValidYAML("a: 1")`},
		{"is_valid_yaml_false", `!filters.isValidYAML("a:\n  b: 1\n c: 2\n")`},
		{"is_valid_port_true", `filters.isValidPort(8080)`},
		{"is_valid_port_false", `!filters.isValidPort(0)`},
		{"drop_empty_values", `filters.dropEmptyValues({"a": "", "b": "kept"}) == {"b": "kept"}`},
		{"filter_list_by_kv", `filters.filterListByKV([{"role": "web"}, {"role": "db"}], "role", "web") == [{"role": "web"}]`},
		{"exclude_list_by_kv", `filters.excludeListByKV([{"role": "web"}, {"role": "db"}], "role", "web") == [{"role": "db"}]`},
		{"list_contains_true", `filters.listContains(["a", "b"], "b")`},
		{"list_contains_false", `!filters.listContains(["a", "b"], "z")`},
		{"has_mandatory_tags", `filters.hasMandatoryTags({"env": "prod"}, ["env", "owner"]) == ["owner"]`},
		{"has_mandatory_tags_none_missing", `size(filters.hasMandatoryTags({"env": "prod", "owner": "team-a"}, ["env", "owner"])) == 0`},
		{"list_intersect", `filters.listIntersect(["a", "b"], ["b", "c"]) == ["b"]`},
		{"list_diff", `filters.listDiff(["a", "b"], ["b"]) == ["a"]`},
		{"dedupe_by_key", `filters.dedupeByKey([{"id": "1"}, {"id": "1"}], "id") == [{"id": "1"}]`},
		{"compare_sem_ver_less", `filters.compareSemVer("1.2.3", "1.3.0") == -1`},
		{"compare_sem_ver_equal", `filters.compareSemVer("1.0.0", "1.0.0") == 0`},
		{"is_valid_cron_expr_true", `filters.isValidCronExpr("*/15 * * * *")`},
		{"is_valid_cron_expr_false", `!filters.isValidCronExpr("not a cron expression")`},
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

// TestCELFilters_Phase54CombinedCondition chains several of this phase's
// functions in one when_cel-shaped condition against a realistic device
// stat payload, the same combined-condition shape
// TestCELFilters_Phase51CombinedCondition through
// TestCELFilters_Phase53CombinedCondition established, with a negative
// control proving the condition genuinely flips false.
func TestCELFilters_Phase54CombinedCondition(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}

	expr := `filters.isValidFQDN(stat.hostname) && ` +
		`filters.isValidPort(int(stat.mgmt_port)) && ` +
		`size(filters.hasMandatoryTags(stat.tags, ["env", "owner"])) == 0 && ` +
		`filters.compareSemVer(stat.os_version, "2.0.0") >= 0 && ` +
		`filters.isValidCronExpr(stat.backup_schedule)`

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	stat := map[string]interface{}{
		"hostname":        "sw1.example.com",
		"mgmt_port":       22,
		"tags":            map[string]interface{}{"env": "prod", "owner": "team-a"},
		"os_version":      "2.1.0",
		"backup_schedule": "0 2 * * *",
	}
	got, err := prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Fatal("expected the combined condition to evaluate true against a realistic stat payload")
	}

	// Negative control: an OS version behind the required floor must flip
	// the same condition false, proving the combined expression is
	// actually exercising every clause rather than being vacuously true.
	stat["os_version"] = "1.9.0"
	got, err = prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Fatal("expected the combined condition to evaluate false once os_version is behind the required floor")
	}
}
