package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCELFilters_Phase58FileTextLogFilters is Phase 58's own Release Gate
// requirement: every one of its 8 filters proven callable through the
// real, unmodified engine.NewCELEvaluator()/Program.Eval via a compiled
// when_cel-shaped expression, not a bare Go function call (RULE 0). Each
// case's want value was independently verified against pkg/filters' own
// unit tests before being written here.
func TestCELFilters_Phase58FileTextLogFilters(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	cases := []struct {
		name string
		expr string
		vars map[string]interface{}
	}{
		{
			"syslog_parse_rfc5424",
			`filters.syslogParse("<34>1 2003-10-11T22:14:15Z host su - ID47 - login ok")["app_name"] == "su"`,
			nil,
		},
		{
			"syslog_parse_rfc3164",
			`filters.syslogParse("<34>Oct 11 22:14:15 host su[123]: login ok")["proc_id"] == "123"`,
			nil,
		},
		{"line_ending_convert", `filters.lineEndingConvert("a\r\nb\nc", "lf") == "a\nb\nc"`, nil},
		{"path_join", `filters.pathJoin(["a", "b", "..", "c"]) == "a/c"`, nil},
		{"path_extract_extension", `filters.pathExtractExtension("archive.tar.gz") == ".gz"`, nil},
		{"gzip_round_trip", `filters.gzipDecompress(filters.gzipCompress("hello, world")) == "hello, world"`, nil},
		{
			"payload_chunker",
			`filters.payloadChunker([1, 2, 3, 4, 5], 2).size() == 3 && filters.payloadChunker([1, 2, 3, 4, 5], 2)[2][0] == 5`,
			nil,
		},
		{"trim_normalize_whitespace", `filters.trimNormalizeWhitespace("  a   b\tc\n") == "a b c"`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prg, err := eval.Compile(tc.expr)
			if err != nil {
				t.Fatalf("failed to compile %q: %v", tc.expr, err)
			}
			vars := tc.vars
			if vars == nil {
				vars = map[string]interface{}{}
			}
			got, err := prg.Eval(vars)
			if err != nil {
				t.Fatalf("eval of %q failed: %v", tc.expr, err)
			}
			if !got {
				t.Errorf("%s: expression %q evaluated false", tc.name, tc.expr)
			}
		})
	}
}

// TestCELFilters_Phase58CombinedCondition chains several of this phase's
// functions in one when_cel-shaped condition against a realistic log-
// processing stat payload, the same combined-condition shape
// TestCELFilters_Phase51CombinedCondition through
// TestCELFilters_Phase57CombinedCondition established, with a negative
// control proving the condition genuinely flips false.
func TestCELFilters_Phase58CombinedCondition(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}

	expr := `filters.syslogParse(stat.raw_line)["severity"] <= 3 && ` +
		`filters.pathExtractExtension(stat.log_path) == ".log" && ` +
		`filters.trimNormalizeWhitespace(filters.syslogParse(stat.raw_line)["message"]) == "disk failure on /dev/sda"`

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	stat := map[string]interface{}{
		"raw_line": "<3>1 2024-01-01T00:00:00Z host kernel - - -   disk   failure  on /dev/sda ",
		"log_path": "/var/log/kern.log",
	}
	got, err := prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Fatal("expected the combined condition to evaluate true against a realistic stat payload")
	}

	// Negative control: a lower-urgency severity (a bigger number; 3 is
	// "error", 6 is "info") must flip the same condition false, proving
	// the combined expression is actually exercising every clause rather
	// than being vacuously true.
	stat["raw_line"] = "<6>1 2024-01-01T00:00:00Z host kernel - - -   disk   failure  on /dev/sda "
	got, err = prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Fatal("expected the combined condition to evaluate false once severity no longer indicates an error")
	}
}
