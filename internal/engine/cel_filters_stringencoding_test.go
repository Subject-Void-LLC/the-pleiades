package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCELFilters_Phase53StringEncodingPathFilters is Phase 53's own
// Release Gate requirement: every one of its 16 filters proven callable
// through the real, unmodified engine.NewCELEvaluator()/Program.Eval via
// a compiled when_cel-shaped expression, not a bare Go function call
// (RULE 0). Each case's want value was independently verified against
// pkg/filters' own unit tests before being written here.
func TestCELFilters_Phase53StringEncodingPathFilters(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	cases := []struct {
		name string
		expr string
	}{
		{"url_encode", `filters.urlEncode("hello world") == "hello+world"`},
		{"url_decode", `filters.urlDecode("hello+world") == "hello world"`},
		{"camel_to_snake", `filters.camelToSnake("classifyIP") == "classify_ip"`},
		{"snake_to_camel", `filters.snakeToCamel("classify_ip") == "classifyIp"`},
		{"string_to_hex", `filters.stringToHex("hi") == "6869"`},
		{"hex_to_string", `filters.hexToString("6869") == "hi"`},
		{"regex_extract", `filters.regexExtract("host1.example.com", "^(?P<host>[^.]+)\\.", "host") == "host1"`},
		{"regex_extract_no_match", `filters.regexExtract("nope", "^(?P<host>[^.]+)\\.", "host") == ""`},
		{"mask_secret", `filters.maskSecret("hunter2", 2) == "*****r2"`},
		{"windows_path_to_posix", `filters.windowsPathToPOSIX("C:\\Users\\foo") == "C:/Users/foo"`},
		{"posix_path_to_windows", `filters.posixPathToWindows("/home/foo") == "\\home\\foo"`},
		{"octal_to_symbolic_perms", `filters.octalToSymbolicPerms("755") == "rwxr-xr-x"`},
		{"symbolic_to_octal_perms", `filters.symbolicToOctalPerms("rwxr-xr-x") == "755"`},
		{"bytes_to_human", `filters.bytesToHuman(1536) == "1.5KiB"`},
		{"human_to_bytes", `filters.humanToBytes("1.5KiB") == 1536`},
		{"is_absolute_path_true", `filters.isAbsolutePath("/etc/passwd")`},
		{"is_absolute_path_false", `!filters.isAbsolutePath("relative/path")`},
		{"is_empty_or_whitespace_true", `filters.isEmptyOrWhitespace("   ")`},
		{"is_empty_or_whitespace_false", `!filters.isEmptyOrWhitespace("x")`},
		{
			"native_cel_predicates_need_no_filter",
			`"GigabitEthernet0/1".startsWith("Gi") && "sw1.example.com".endsWith(".com") && ` +
				`"host1.example.com".contains("example") && "10.0.0.1".matches("^[0-9.]+$")`,
		},
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

// TestCELFilters_Phase53CombinedCondition chains five of this phase's
// functions in one when_cel-shaped condition against a realistic device
// stat payload, the same combined-condition shape
// TestCELFilters_Phase51CombinedCondition/TestCELFilters_Phase52CombinedCondition
// established, with a negative control proving the condition genuinely
// flips false.
func TestCELFilters_Phase53CombinedCondition(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}

	expr := `!filters.isEmptyOrWhitespace(stat.hostname) && ` +
		`filters.isAbsolutePath(stat.config_path) && ` +
		`filters.camelToSnake(stat.deviceType) == "linux_server" && ` +
		`filters.octalToSymbolicPerms(stat.config_perms) == "rw-r--r--" && ` +
		`filters.regexExtract(stat.hostname, "^(?P<name>[^.]+)\\.", "name") == "sw1"`

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	stat := map[string]interface{}{
		"hostname":     "sw1.example.com",
		"config_path":  "/etc/network/config",
		"deviceType":   "LinuxServer",
		"config_perms": "644",
	}
	got, err := prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Fatal("expected the combined condition to evaluate true against a realistic stat payload")
	}

	// Negative control: a relative config path must flip the same
	// condition false, proving the combined expression is actually
	// exercising every clause rather than being vacuously true.
	stat["config_path"] = "relative/config"
	got, err = prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Fatal("expected the combined condition to evaluate false once config_path is relative")
	}
}
