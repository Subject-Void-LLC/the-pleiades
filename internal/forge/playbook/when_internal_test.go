// Tests for when: translation, run through the engine's own CEL
// evaluator: every emitted expression is compiled and evaluated against
// the stat shape the executor builds, so a translation that compiles but
// means something else fails here.
package playbook

import (
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// whenTranslator is a translator whose index holds the variables the
// table uses, and whose registers hold probe, an exec.command result.
func whenTranslator(t *testing.T) *translator {
	t.Helper()
	tr := &translator{file: "pb.yml", ix: newVarIndex(), seen: map[findingKey]string{}, registers: map[string]produced{}}
	tr.res = newResolver(tr.ix)
	var vars yaml.Node
	if err := yaml.Unmarshal([]byte("enabled: yes\ncount: 3\nname: web\nlist: [a, b]\ndb_password: hunter2hunter2\ntwice: 1\n"), &vars); err != nil {
		t.Fatal(err)
	}
	tr.ix.addLiterals(vars.Content[0], "pb.yml")
	tr.ix.addRuntime("twice", Position{})
	tr.ix.addRegister("probe", "command", Position{})
	tr.registers["probe"] = produced{fqcn: "exec.command", returns: commandReturns}
	return tr
}

// evalCEL compiles cel with the engine's evaluator and evaluates it over
// a stat holding probe's result on two devices.
func evalCEL(t *testing.T, cel string, rc0, rc1 int64, stdout string) bool {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	prg, err := eval.Compile(cel)
	if err != nil {
		t.Fatalf("emitted CEL %q does not compile: %v", cel, err)
	}
	stat := map[string]any{"probe": map[string]any{
		"dev1": map[string]any{"rc": rc0, "stdout": stdout, "stderr": ""},
		"dev2": map[string]any{"rc": rc1, "stdout": stdout, "stderr": ""},
	}}
	ok, err := prg.Eval(map[string]any{"stat": stat, "nodes": map[string]any{}, "vars": map[string]any{}})
	if err != nil {
		t.Fatalf("emitted CEL %q does not evaluate: %v", cel, err)
	}
	return ok
}

// TestWhen_ToCEL covers the translated subset: constants folded with
// Python's rules, and registered reads that compile, evaluate over every
// device, and mean what the Jinja meant.
func TestWhen_ToCEL(t *testing.T) {
	tr := whenTranslator(t)
	for _, tc := range []struct {
		src  string
		want string // "" always true, "false" always false, else must evaluate as the cases say
		// on is the result with rc 0 on both devices and stdout "Verified OK";
		// off with rc 1 on the second device and stdout "nope".
		on, off  bool
		register bool
	}{
		{src: "enabled", want: ""},
		{src: "enabled | bool", want: ""},
		{src: "not enabled", want: "false"},
		{src: "count > 2 and name == 'web'", want: ""},
		{src: "count == 3.0", want: ""},
		{src: "'a' in list", want: ""},
		{src: "'z' not in list", want: ""},
		{src: "list | length == 2", want: ""},
		{src: "missing is defined", want: "false"},
		{src: "missing is not defined", want: ""},
		{src: "(enabled or count < 0) and not (name != 'web')", want: ""},
		{src: "probe.rc == 0", on: true, off: false, register: true},
		{src: "probe.rc != 0", on: false, off: false, register: true},
		{src: "'Verified' in probe.stdout", on: true, off: false, register: true},
		{src: "probe.stdout", on: true, off: true, register: true},
		{src: "probe is defined", on: true, off: true, register: true},
		{src: "probe.stdout | length > 3 and enabled", on: true, off: true, register: true},
	} {
		t.Run(tc.src, func(t *testing.T) {
			cel, reads, cerr := tr.condition(tc.src, nil, Position{})
			if cerr != nil {
				t.Fatalf("refused: %v", cerr)
			}
			if reads != tc.register {
				t.Errorf("readsRegister = %v, want %v", reads, tc.register)
			}
			if !tc.register {
				if cel != tc.want {
					t.Errorf("CEL = %q, want %q", cel, tc.want)
				}
				return
			}
			if got := evalCEL(t, cel, 0, 0, "Verified OK"); got != tc.on {
				t.Errorf("%s over rc 0 and Verified = %v, want %v", cel, got, tc.on)
			}
			if got := evalCEL(t, cel, 0, 1, "nope"); got != tc.off {
				t.Errorf("%s over rc 0/1 and nope = %v, want %v", cel, got, tc.off)
			}
		})
	}
}

// TestWhen_Refused covers what is refused, and with which code.
func TestWhen_Refused(t *testing.T) {
	tr := whenTranslator(t)
	for _, tc := range []struct {
		src  string
		code Code
	}{
		{"ansible_os_family == 'Debian'", "when.fact"},
		{"inventory_hostname in groups['web']", "when.fact"},
		{"probe.changed", "when.unsupported"},
		{"probe.rc == probe.rc", "when.unsupported"},
		{"probe.rc == 'zero'", "when.unsupported"},
		{"twice == 1", "when.unsupported"},
		{"db_password == 'x'", "template.secret"},
		{"x | default(true)", "when.unsupported"},
		{"count + 1 > 2", "when.unsupported"},
		{"{{ enabled }}", "when.unsupported"},
		{"probe.rc is number", "when.unsupported"},
		{"'a' ~ 'b'", "when.unsupported"},
		{"and", "when.unsupported"},
		{strings.Repeat("(", 40) + "enabled" + strings.Repeat(")", 40), "when.unsupported"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			_, _, cerr := tr.condition(tc.src, nil, Position{})
			if cerr == nil || cerr.code != tc.code {
				t.Errorf("refusal = %v, want code %s", cerr, tc.code)
			}
		})
	}
}

// FuzzWhenToCEL feeds arbitrary conditions to the translator. It must
// never panic; whatever it emits must compile; and a string literal must
// reach CEL as exactly that string, which is the property that keeps
// playbook text from being spliced into an expression: the fuzzer builds
// a Jinja literal from arbitrary text and the emitted CEL must match a
// stdout holding exactly that text, and nothing else.
func FuzzWhenToCEL(f *testing.F) {
	for _, seed := range []string{"probe.rc == 0", "enabled and count > 1", `probe.stdout == 'a"b'`, "not not (x is defined)", "[1, 2] | length", "'\\\\' in probe.stdout"} {
		f.Add(seed, "text")
	}
	f.Fuzz(func(t *testing.T, src, text string) {
		tr := whenTranslator(t)
		if cel, _, cerr := tr.condition(src, nil, Position{}); cerr == nil && cel != "" && cel != "false" {
			evalCEL(t, cel, 0, 0, "x")
		}
		quoted := "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(text) + "'"
		if strings.ContainsAny(text, "\n\t") || !isPrintable(text) {
			return
		}
		cel, _, cerr := tr.condition("probe.stdout == "+quoted, nil, Position{})
		if cerr != nil {
			t.Fatalf("literal %s refused: %v", strconv.Quote(text), cerr)
		}
		if !evalCEL(t, cel, 0, 0, text) {
			t.Fatalf("CEL %q does not match its own literal %s", cel, strconv.Quote(text))
		}
		if text != "" && evalCEL(t, cel, 0, 0, text+"x") {
			t.Fatalf("CEL %q matches text other than its literal", cel)
		}
	})
}

// isPrintable reports whether every rune in s is printable and valid.
func isPrintable(s string) bool {
	for _, r := range s {
		if !strconv.IsPrint(r) {
			return false
		}
	}
	return strings.ToValidUTF8(s, "") == s
}

// TestWhen_MoreCEL covers the rest of the subset, each registered read
// compiled and evaluated by the engine: folding one side of and/or away,
// a constant on the left, list membership, the truth of each field type,
// a register whose name CEL cannot read with a dot, and the filters. ""
// is always true and "false" always false, as condition returns them.
func TestWhen_MoreCEL(t *testing.T) {
	tr := whenTranslator(t)
	tr.ix.addRegister("null", "command", Position{})
	tr.registers["null"] = produced{fqcn: "exec.command", returns: commandReturns}
	for _, tc := range []struct {
		src     string
		on, off bool
	}{
		{"enabled and probe.rc == 0", true, false},
		{"not enabled and probe.rc == 0", false, false},
		{"probe.rc == 0 and enabled", true, false},
		{"probe.rc == 0 or not enabled", true, false},
		{"probe.rc == 0 or enabled", true, true},
		{"not enabled or probe.rc == 0", true, false},
		{"probe.rc == 0 and probe.stdout != ''", true, false},
		{"probe.rc == 0 or probe.stdout == 'nope'", true, true},
		{"0 == probe.rc", true, false},
		{"1 > probe.rc", true, false},
		{"probe.rc <= 0", true, false},
		{"probe.rc in [0, 2]", true, false},
		{"probe.rc not in [1]", true, false},
		{"probe['rc'] == 0", true, false},
		{"probe.rc", false, false},
		{"not probe.rc", true, false},
		{"probe.cmd", true, true},
		{"'OK' not in probe.stdout", false, true},
		{"probe.stdout > 'A'", true, true},
		{"probe.rc | int == 0", true, false},
		{"probe is defined and probe.rc == 0", true, false},
		{"not (probe.rc == 0)", false, false},
		{"null.rc == 0", true, false},
		{"'a' in []", false, false},
		{"list[1] == 'b' and probe.rc == 0", true, false},
		{"count > -1 and probe.rc == 0", true, false},
		{"'5' | int > 4 and probe.rc == 0", true, false},
		{"'it\\'s' != \"x\\ty\" and probe.rc == 0", true, false},
	} {
		t.Run(tc.src, func(t *testing.T) {
			cel, _, cerr := tr.condition(tc.src, nil, Position{})
			if cerr != nil {
				t.Fatalf("refused: %v", cerr)
			}
			eval := func(rc1 int64, stdout string) bool {
				switch cel {
				case "":
					return true
				case "false":
					return false
				}
				stat := map[string]any{"dev1": map[string]any{"rc": int64(0), "stdout": stdout, "stderr": "", "cmd": "c"}, "dev2": map[string]any{"rc": rc1, "stdout": stdout, "stderr": "", "cmd": "c"}}
				return evalStat(t, cel, map[string]any{"probe": stat, "null": stat})
			}
			if got := eval(0, "Verified OK"); got != tc.on {
				t.Errorf("%s over rc 0 and Verified = %v, want %v", cel, got, tc.on)
			}
			if got := eval(1, "nope"); got != tc.off {
				t.Errorf("%s over rc 0/1 and nope = %v, want %v", cel, got, tc.off)
			}
		})
	}
}

// evalStat compiles cel with the engine's evaluator and evaluates it over
// stat.
func evalStat(t *testing.T, cel string, stat map[string]any) bool {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	prg, err := eval.Compile(cel)
	if err != nil {
		t.Fatalf("emitted CEL %q does not compile: %v", cel, err)
	}
	ok, err := prg.Eval(map[string]any{"stat": stat, "nodes": map[string]any{}, "vars": map[string]any{}})
	if err != nil {
		t.Fatalf("emitted CEL %q does not evaluate: %v", cel, err)
	}
	return ok
}

// TestWhen_MoreRefused covers the rest of what is refused: reads and
// comparisons with no faithful CEL, tests and filters outside the subset,
// and malformed conditions, each named by kind rather than by value.
func TestWhen_MoreRefused(t *testing.T) {
	tr := whenTranslator(t)
	tr.ix.addRegister("null", "command", Position{})
	tr.registers["null"] = produced{fqcn: "exec.command", returns: commandReturns}
	tr.ix.addRegister("loose", "command", Position{})
	tr.registers["loose"] = produced{fqcn: "exec.command", returns: map[string]string{"x": "not_a_field"}}
	for _, tc := range []struct {
		src  string
		code Code
	}{
		{"probe", "when.unsupported"},
		{"probe[0] == 1", "when.unsupported"},
		{"probe.rc.x == 1", "when.unsupported"},
		{"probe.rc is defined", "when.unsupported"},
		{"probe.rc | bool", "when.unsupported"},
		{"probe.stdout == 1", "when.unsupported"},
		{"probe.rc == none", "when.unsupported"},
		{"probe.stdout in 'abc'", "when.unsupported"},
		{"probe.rc in probe.rc", "when.unsupported"},
		{"[probe.rc] | length", "when.unsupported"},
		{"loose.x", "when.unsupported"},
		{"loose.x == 1", "when.unsupported"},
		{"name | int", "when.unsupported"},
		{"count | length", "when.unsupported"},
		{"list[5] == 'a'", "when.unsupported"},
		{"name.x == 1", "when.unsupported"},
		{"list.x == 1", "when.unsupported"},
		{"name.x is defined", "when.unsupported"},
		{"'x' is defined", "when.unsupported"},
		{"twice is defined", "when.unsupported"},
		{"ansible_os_family is defined", "when.fact"},
		{"(enabled", "when.unsupported"},
		{"[1 2]", "when.unsupported"},
		{"list[x]", "when.unsupported"},
		{"list[0", "when.unsupported"},
		{"list.", "when.unsupported"},
		{"3.4.5 > 1", "when.unsupported"},
		{"enabled enabled", "when.unsupported"},
		{"enabled 'x'", "when.unsupported"},
		{"count - 1 > 0", "when.unsupported"},
		{"'a\\q' == x", "when.unsupported"},
		{"'open", "when.unsupported"},
		{"'trailing\\", "when.unsupported"},
		{"x is 'odd'", "when.unsupported"},
		{"x is", "when.unsupported"},
		{"x | unknown_filter", "when.unsupported"},
		{"probe is not defined", "when.unsupported"},
		{"not (probe is defined)", "when.unsupported"},
		{"not (probe is not defined)", ""},
		{"(probe.rc == 0) == false", "when.unsupported"},
		{"probe.rc == 0 and null.rc == 0", "when.unsupported"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			_, _, cerr := tr.condition(tc.src, nil, Position{})
			switch {
			case tc.code == "" && cerr != nil:
				t.Errorf("refused: %v", cerr)
			case tc.code != "" && (cerr == nil || cerr.code != tc.code):
				t.Errorf("refusal = %v, want code %s", cerr, tc.code)
			}
		})
	}
	for tok, want := range map[string]string{"": "the end", "'s'": "a text literal", "\"s\"": "a text literal", "12": "a number", "-3": "a number", "-": `"-"`, "and": `"and"`} {
		if got := describeToken(tok); got != want {
			t.Errorf("describeToken(%q) = %s, want %s", tok, got, want)
		}
	}
}
