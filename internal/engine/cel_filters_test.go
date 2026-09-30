package engine_test

import (
	"strings"
	"testing"

	"cel.dev/cel-go/cel"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCELFilters_SafeInt proves filters.safeInt is reachable through the
// real, unmodified engine.NewCELEvaluator()/Program.Eval path, not just a
// bare pkg/filters.SafeInt call, and that it behaves identically whether
// the underlying stat field is already a CEL int (a device that reports a
// count natively) or a string (the same field on a different device or
// firmware): both must reach the identical parsed result, which is the
// entire point of the dyn-typed first argument.
func TestCELFilters_SafeInt(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	prg, err := eval.Compile(`filters.safeInt(stat.retries, 0) > 3`)
	if err != nil {
		t.Fatalf("failed to compile expression: %v", err)
	}

	cases := []struct {
		name    string
		retries interface{}
		want    bool
	}{
		{"string_above_threshold", "7", true},
		{"string_below_threshold", "2", false},
		{"native_int_above_threshold", 7, true},
		{"malformed_falls_back_to_zero", "not-a-number", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := prg.Eval(map[string]interface{}{
				"stat": map[string]interface{}{"retries": tc.retries},
			})
			if err != nil {
				t.Fatalf("eval failed: %v", err)
			}
			if got != tc.want {
				t.Errorf("filters.safeInt(stat.retries, 0) > 3 with retries=%v: got %v, want %v", tc.retries, got, tc.want)
			}
		})
	}
}

// TestCELFilters_SafeFloatAndSafeBool covers the remaining two cast
// filters through the same real Compile/Eval path.
func TestCELFilters_SafeFloatAndSafeBool(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	floatPrg, err := eval.Compile(`filters.safeFloat(stat.load, 0.0) > 3.5`)
	if err != nil {
		t.Fatalf("failed to compile safeFloat expression: %v", err)
	}
	got, err := floatPrg.Eval(map[string]interface{}{"stat": map[string]interface{}{"load": "4.2"}})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Errorf("expected filters.safeFloat('4.2', 0.0) > 3.5 to be true")
	}
	got, err = floatPrg.Eval(map[string]interface{}{"stat": map[string]interface{}{"load": "NaN"}})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Errorf("expected filters.safeFloat('NaN', 0.0) > 3.5 to be false (NaN falls back)")
	}

	boolPrg, err := eval.Compile(`filters.safeBool(stat.enabled, false)`)
	if err != nil {
		t.Fatalf("failed to compile safeBool expression: %v", err)
	}
	got, err = boolPrg.Eval(map[string]interface{}{"stat": map[string]interface{}{"enabled": "yes"}})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Errorf("expected filters.safeBool('yes', false) to be true")
	}
	got, err = boolPrg.Eval(map[string]interface{}{"stat": map[string]interface{}{"enabled": "maybe"}})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Errorf("expected filters.safeBool('maybe', false) to fall back to false")
	}
}

// TestCELFilters_NetworkAndEncodersAndOptional proves the three cel-go
// standard extensions Phase 50 wires (ext.Network, ext.Encoders,
// cel.OptionalTypes) are reachable through the same real path, not just
// present in go.mod. This is the "no phase builds a hand-rolled
// default/mandatory function" claim's own positive proof: orValue really
// does solve the missing-value case.
func TestCELFilters_NetworkAndEncodersAndOptional(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	cases := []struct {
		name string
		expr string
		stat map[string]interface{}
		want bool
	}{
		{
			name: "network_cidr_contains_ip",
			expr: `cidr('10.0.0.0/8').containsIP(ip(stat.mgmt_ip))`,
			stat: map[string]interface{}{"mgmt_ip": "10.0.0.5"},
			want: true,
		},
		{
			name: "network_is_ip",
			expr: `isIP(stat.mgmt_ip)`,
			stat: map[string]interface{}{"mgmt_ip": "10.0.0.5"},
			want: true,
		},
		{
			name: "encoders_base64_roundtrip",
			// base64.decode returns bytes, not string: the reference
			// documentation calls this out explicitly (finding 2 of the
			// phase's own spec scope check).
			expr: `string(base64.decode(stat.blob)) == 'ok'`,
			stat: map[string]interface{}{"blob": "b2s="}, // base64("ok")
			want: true,
		},
		{
			name: "encoders_json_encode_nonempty",
			expr: `json.encode(stat) != ''`,
			stat: map[string]interface{}{"anything": "here"},
			want: true,
		},
		{
			name: "optional_or_value_on_missing_field",
			expr: `stat.?missing.orValue(0) == 0`,
			stat: map[string]interface{}{"present": "x"},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prg, err := eval.Compile(tc.expr)
			if err != nil {
				t.Fatalf("failed to compile %q: %v", tc.expr, err)
			}
			got, err := prg.Eval(map[string]interface{}{"stat": tc.stat})
			if err != nil {
				t.Fatalf("eval of %q failed: %v", tc.expr, err)
			}
			if got != tc.want {
				t.Errorf("%s: got %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestCELFilters_FullEnvironmentHasNoCollision is the real, structural
// proof PLAN.md Section 36's "no collision is possible" claim asks for:
// building the exact option set NewCELEvaluator uses (three variables plus
// ext.Network, ext.Encoders, cel.OptionalTypes and filtersLib) into one
// cel.Env must succeed, since cel-go's own function-declaration merge
// errors loudly on any name collision.
//
// This is paired with a negative control, per AGENTS.md's own "always run
// a control first" rule applied to a test rather than an LSP query: a
// passing collision-free assertion proves nothing about whether cel-go
// would actually have caught a real collision unless something in this
// same test also proves a genuine collision does fail. Registering
// filters.safeInt's own overload ID a second time is exactly such a
// collision, deliberately constructed here, that must produce an error.
func TestCELFilters_FullEnvironmentHasNoCollision(t *testing.T) {
	opts := append(engine.CELVariableOptions(), engine.CELLibraryOptions()...)
	if _, err := cel.NewEnv(opts...); err != nil {
		t.Fatalf("expected the real environment to build with no collision, got: %v", err)
	}

	// Negative control: deliberately register a second overload of
	// filters.safeInt with the identical (dyn, int) -> int signature
	// under a different overload ID, and confirm cel.NewEnv actually
	// rejects it as a signature collision. This is the real thing
	// PLAN.md Section 36's "no collision is possible" claim depends on:
	// re-registering the exact same overload ID with an identical
	// signature is cel-go's own documented idempotent redefinition (not a
	// collision at all, confirmed by reading common/decls.AddOverload),
	// so that case would prove nothing here. An overlapping signature
	// under a distinct ID is the shape a real accidental collision would
	// actually take, and it is what AddOverload's own
	// "overload signature collision" branch exists to catch.
	dupeOpts := append(engine.CELVariableOptions(), engine.CELLibraryOptions()...)
	dupeOpts = append(dupeOpts,
		cel.Function("filters.safeInt",
			cel.Overload("filters_safe_int_dyn_int_conflicting",
				[]*cel.Type{cel.DynType, cel.IntType}, cel.IntType,
			),
		),
	)
	if _, err := cel.NewEnv(dupeOpts...); err == nil {
		t.Fatal("expected cel.NewEnv to reject an overlapping filters.safeInt overload signature, got no error (control failed: collision detection is not actually being exercised)")
	}
}

// TestCELFilters_CostLimitStillTerminatesWithFilters is the phase's own
// stress case: several registered filters chained inside a comprehension
// over a large-but-bounded stat payload must still be rejected by
// defaultCELCostLimit, the same way TestCELEngine_RejectsExpensiveComprehension
// already proves for built-in operators. A custom cel.Function with no
// registered cost estimator is charged a flat cost of 1 per call
// regardless of argument size, so this also confirms that flat charge
// does not let a filter-heavy expression evade the limit a plain
// comprehension already cannot.
func TestCELFilters_CostLimitStillTerminatesWithFilters(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	const n = 3200
	items := make([]string, n)
	for i := range items {
		items[i] = `"7"`
	}
	list := "[" + strings.Join(items, ",") + "]"
	expr := list + `.all(x, ` + list + `.all(y, filters.safeInt(x, 0) + filters.safeInt(y, 0) >= 0))`

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("expected this expression to compile (well within cel-go's own size limit): %v", err)
	}

	_, err = prg.Eval(map[string]interface{}{})
	if err == nil {
		t.Fatal("expected the cost limit to reject this expression even with filters.safeInt in the inner loop")
	}
}

// TestCELFilters_RealisticExpressionStaysUnderCostLimit is the positive
// control for the stress test above: an ordinary, realistic when_cel
// condition chaining several filters must NOT be rejected by the cost
// limit, proving the limit is not so tight it makes the feature this
// phase ships unusable.
func TestCELFilters_RealisticExpressionStaysUnderCostLimit(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	expr := `filters.safeInt(stat.retries, 0) > 3 && filters.safeBool(stat.enabled, false) && isIP(stat.mgmt_ip)`
	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}
	_, err = prg.Eval(map[string]interface{}{
		"stat": map[string]interface{}{"retries": "7", "enabled": "yes", "mgmt_ip": "10.0.0.5"},
	})
	if err != nil {
		t.Fatalf("expected a realistic filter-chained expression to stay under the cost limit, got: %v", err)
	}
}

// TestCELFilters_OverLengthInputFallsBackPromptly is the Schema/Injection
// Hardening case: a filter given a megabyte-scale stat value must return
// its fallback (MaxInputBytes rejects it before any parsing), not hang or
// error, since defaultCELCostLimit charges a flat cost of 1 for the call
// regardless of the argument's own size and so cannot be relied on to
// catch this by itself.
func TestCELFilters_OverLengthInputFallsBackPromptly(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	prg, err := eval.Compile(`filters.safeInt(stat.big, -1) == -1`)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	huge := strings.Repeat("1", 5_000_000)
	got, err := prg.Eval(map[string]interface{}{"stat": map[string]interface{}{"big": huge}})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Fatal("expected an over-length input to fall back rather than being parsed")
	}
}
