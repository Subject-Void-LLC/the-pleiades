// Package render_test: the expression language against Jinja's answers,
// its render-time errors, its compile refusals and what it reports reading.
package render_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// bs is one backslash, so a test can spell an escape without the source
// holding it literally.
const bs = "\\"

// TestExpressionOperators renders each literal, operator and test with the
// value Jinja2 gives for the same expression and variables.
func TestExpressionOperators(t *testing.T) {
	t.Parallel()
	vars := map[string]any{
		"env": "prod", "n": 3, "f": 2.5, "yes": true, "no": false, "nil": nil, "empty": "",
		"l": []any{"a", "b"}, "m": map[string]any{"k": 1, "j": "x"}, "nums": []any{1, 2},
	}
	tests := []struct{ source, want string }{
		{"{{ 'it' ~ \"s\" }}", "its"},
		{"{{ 'a" + bs + "tb' }}", "a\tb"},
		{"{{ '" + bs + "x41" + bs + "101" + bs + "u00e9" + bs + "U0001F600' }}", "AAé\U0001F600"},
		{"{{ 42 }} {{ -7 }} {{ 0x1f }} {{ 0o17 }} {{ 0b101 }} {{ 1_000 }}", "42 -7 31 15 5 1000"},
		{"{{ 1.5 }} {{ 2e3 }} {{ -0.25 }}", "1.5 2000 -0.25"},
		{"{{ true }} {{ False }}", "true false"},
		{"{{ [1, 'a', none] | to_json }} {{ (1, 2) | to_json }} {{ (1,) | to_json }} {{ () | to_json }}", `[1,"a",null] [1,2] [1] []`},
		{"{{ {'a': [1], \"b\": {'c': true}} | to_json }}", `{"a":[1],"b":{"c":true}}`},
		{"{{ env == 'prod' }} {{ env != 'prod' }} {{ n == '3' }} {{ n == 3.0 }} {{ yes == 1 }} {{ nil == none }}", "true false false true true true"},
		{"{{ n > 2 }} {{ n <= 2 }} {{ f < n }} {{ 'a' < 'b' }} {{ [1, 2] < [1, 3] }} {{ [1] < [1, 0] }}", "true false true true true true"},
		{"{{ 1 < n < 5 }} {{ 1 < n < 2 }}", "true false"},
		{"{{ 'a' in l }} {{ 'c' not in l }} {{ 'k' in m }} {{ 1 in m }} {{ 'ro' in env }} {{ 2 in nums }} {{ 2.0 in nums }}", "true true true false true true true"},
		{"{{ m.keys() | to_json }} {{ m.values() | to_json }} {{ m.items() | to_json }}", `["j","k"] ["x",1] [["j","x"],["k",1]]`},
		{"{{ empty or 'fallback' }} {{ env and n }} {{ no or nil or 'last' }} {{ yes and no }}", "fallback 3 last false"},
		{"{{ not yes }} {{ not not env }} {{ not n == 3 }}", "false true false"},
		{"{{ yes or missing }} {{ no and missing }}", "true false"},
		{"{{ env is defined }} {{ missing is defined }} {{ missing is not defined }} {{ m.k is defined }} {{ m.zz is defined }}", "true false true true false"},
		{"{{ nil is none }} {{ env is string }} {{ n is number }} {{ yes is number }} {{ env is number }}", "true true true true false"},
		{"{{ m is mapping }} {{ l is mapping }} {{ l is iterable }} {{ env is iterable }} {{ n is iterable }}", "true false true true false"},
		{"{{ no is sameas false }} {{ no is sameas(false) }} {{ 0 is sameas false }} {{ nil is sameas none }}", "true true false true"},
		{"{{ (yes or no) and env == 'prod' }} {{ yes or no and no }}", "true true"},
		{"{{ env ~ '-' ~ n }} {{ env ~ l[0] | upper }}", "prod-3 prodA"},
		{"{{ missing | default('d') ~ '!' }} {{ missing | default(env) }}", "d! prod"},
	}
	eng := render.New()
	for _, tt := range tests {
		tmpl, err := eng.Compile(tt.source)
		if err != nil {
			t.Errorf("Compile(%q): %v", tt.source, err)
			continue
		}
		got, err := tmpl.Render(vars)
		if err != nil || got != tt.want {
			t.Errorf("Render(%q) = %q, %v; want %q", tt.source, got, err, tt.want)
		}
	}
}

// TestExpressionRenderErrors covers what an operator refuses when it runs:
// strict undefined on the branch evaluated, and Python's type errors.
func TestExpressionRenderErrors(t *testing.T) {
	t.Parallel()
	vars := map[string]any{"n": 3, "s": "x", "m": map[string]any{}, "l": []any{1}, "yes": true, "nil": nil}
	tests := []struct {
		source  string
		wantErr error
	}{
		{"{{ no_such == 1 }}", render.ErrUndefined},
		{"{{ yes and missing }}", render.ErrUndefined},
		{"{{ missing or yes }}", render.ErrUndefined},
		{"{{ n < 1 < missing }}", nil},
		{"{{ 1 < n < missing }}", render.ErrUndefined},
		{"{{ n < s }}", render.ErrNotComparable},
		{"{{ m < m }}", render.ErrNotComparable},
		{"{{ nil < 1 }}", render.ErrNotComparable},
		{"{{ 1 in s }}", render.ErrNotComparable},
		{"{{ l in m }}", render.ErrNotComparable},
		{"{{ 1 in n }}", render.ErrNotIterable},
		{"{{ s ~ nil }}", render.ErrNotRenderable},
		{"{{ s ~ l }}", render.ErrNotRenderable},
		{"{{ n.items() }}", render.ErrNotIterable},
	}
	eng := render.New()
	for _, tt := range tests {
		tmpl, err := eng.Compile(tt.source)
		if err != nil {
			t.Errorf("Compile(%q): %v", tt.source, err)
			continue
		}
		got, err := tmpl.Render(vars)
		if tt.wantErr == nil {
			if err != nil {
				t.Errorf("Render(%q): %v, want the short-circuit to skip the undefined name", tt.source, err)
			}
			continue
		}
		if !errors.Is(err, tt.wantErr) || got != "" {
			t.Errorf("Render(%q) = %q, %v; want %v", tt.source, got, err, tt.wantErr)
		}
	}
}

// TestExpressionRefusals covers the forms refused at compile time.
func TestExpressionRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, source string
		wantErr      error
		mention      string
	}{
		{"addition", "{{ a + 1 }}", render.ErrSyntax, "arithmetic"},
		{"multiplication", "{{ 'a' * 9 }}", render.ErrSyntax, "arithmetic"},
		{"negation of a name", "{{ -a }}", render.ErrSyntax, "arithmetic"},
		{"an inline if", "{{ a if b else c }}", render.ErrSyntax, "inline if"},
		{"a computed subscript", "{{ a[b] }}", render.ErrSyntax, "literal"},
		{"a method call", "{{ a.upper() }}", render.ErrSyntax, "calling"},
		{"an unknown test", "{{ a is even }}", render.ErrSyntax, "even"},
		{"sameas with no argument", "{{ a is sameas }}", render.ErrSyntax, ""},
		{"defined with an argument", "{{ a is defined(1) }}", render.ErrSyntax, "defined"},
		{"a leading zero", "{{ 012 }}", render.ErrSyntax, "start with 0"},
		{"a double underscore", "{{ 1__0 }}", render.ErrSyntax, "number"},
		{"a number out of range", "{{ 99999999999999999999 }}", render.ErrSyntax, "range"},
		{"a float out of range", "{{ 1e999 }}", render.ErrSyntax, "range"},
		{"a short hex escape", "{{ '" + bs + "x4' }}", render.ErrSyntax, "hex"},
		{"a surrogate escape", "{{ '" + bs + "ud800' }}", render.ErrSyntax, ""},
		{"a named escape", "{{ '" + bs + "N{BULLET}' }}", render.ErrSyntax, ""},
		{"an unquoted mapping key", "{{ {a: 1} }}", render.ErrSyntax, "quoted"},
		{"a repeated mapping key", "{{ {'a': 1, 'a': 2} }}", render.ErrSyntax, "twice"},
		{"an unclosed list", "{{ [1, 2 }}", render.ErrSyntax, ""},
		{"a keyword as a value", "{{ and }}", render.ErrSyntax, "and"},
		{"a dangling operator", "{{ a == }}", render.ErrSyntax, ""},
		{"a literal past the bound", "{{ [" + strings.Repeat("1,", 257) + "] }}", render.ErrTooLarge, "256"},
		{"nesting past the bound", "{{ " + strings.Repeat("(", 33) + "a" + strings.Repeat(")", 33) + " }}", render.ErrTooLarge, "32"},
		{"nots past the bound", "{{ " + strings.Repeat("not ", 33) + "a }}", render.ErrTooLarge, "32"},
	}
	eng := render.New()
	for _, tt := range tests {
		tmpl, err := eng.Compile(tt.source)
		if err == nil || tmpl != nil {
			t.Errorf("%s: Compile(%q) succeeded", tt.name, tt.source)
			continue
		}
		if !errors.Is(err, tt.wantErr) || !strings.Contains(err.Error(), tt.mention) {
			t.Errorf("%s: Compile(%q) error %v, want %v naming %q", tt.name, tt.source, err, tt.wantErr, tt.mention)
		}
	}
	for _, ok := range []string{"{{ [" + strings.Repeat("1,", 256) + "] }}", "{{ " + strings.Repeat("(", 31) + "a" + strings.Repeat(")", 31) + " }}"} {
		if _, err := eng.Compile(ok); err != nil {
			t.Errorf("a template at the limit failed to compile: %v", err)
		}
	}
}

// TestExpressionReads holds what callers' rules read from an expression:
// every path it touches, a read that only decides a truth reported as a
// condition, the filters after each read, and guarded reads.
func TestExpressionReads(t *testing.T) {
	t.Parallel()
	type info = render.ExpressionInfo
	out, cond, assign := render.ExpressionOutput, render.ExpressionCondition, render.ExpressionAssignment
	tests := []struct {
		source string
		want   []info
	}{
		{"{{ vars.env == 'prod' }}", []info{{Path: []string{"vars", "env"}, Kind: cond}}},
		{"{{ (vars.a ~ nodes.r.x) | quote }}", []info{{Path: []string{"vars", "a"}, Filters: []string{"quote"}, Kind: out}, {Path: []string{"nodes", "r", "x"}, Filters: []string{"quote"}, Kind: out}}},
		{"{{ vars.a | quote ~ vars.b }}", []info{{Path: []string{"vars", "a"}, Filters: []string{"quote"}, Kind: out}, {Path: []string{"vars", "b"}, Kind: out}}},
		{"{{ vars.a or vars.b }}", []info{{Path: []string{"vars", "a"}, Kind: out}, {Path: []string{"vars", "b"}, Kind: out}}},
		{"{{ result.x is defined and result.x.ok }}", []info{{Path: []string{"result", "x"}, Kind: cond, Guarded: true}, {Path: []string{"result", "x", "ok"}, Kind: out, Guarded: true}}},
		{"{{ vars.a | default(vars.b) | quote }}", []info{{Path: []string{"vars", "a"}, Filters: []string{"default", "quote"}, Kind: out, Guarded: true}, {Path: []string{"vars", "b"}, Filters: []string{"quote"}, Kind: out}}},
		{"{{ 'x' in vars.l }}{{ not vars.n }}{{ vars.s is string }}", []info{{Path: []string{"vars", "l"}, Kind: cond}, {Path: []string{"vars", "n"}, Kind: cond}, {Path: []string{"vars", "s"}, Kind: cond}}},
		{"{{ [vars.a, {'k': vars.b}] | to_json }}", []info{{Path: []string{"vars", "a"}, Filters: []string{"to_json"}, Kind: out}, {Path: []string{"vars", "b"}, Filters: []string{"to_json"}, Kind: out}}},
		{"{{ vars.m.items() | to_json }}", []info{{Path: []string{"vars", "m"}, Filters: []string{"to_json"}, Kind: out}}},
		{"{% set on = vars.env == 'prod' %}{{ on }}", []info{{Path: []string{"vars", "env"}, Kind: cond}}},
		{"{% set h = vars.host or 'x' %}{{ h }}", []info{{Path: []string{"vars", "host"}, Kind: assign}, {Path: []string{"vars", "host"}, Kind: out}}},
		{"{{ 'literal' }}{{ 1 == 2 }}", nil},
	}
	eng := render.New()
	for _, tt := range tests {
		tmpl, err := eng.Compile(tt.source)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tt.source, err)
		}
		if got := tmpl.Expressions(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Expressions(%q) = %#v\nwant %#v", tt.source, got, tt.want)
		}
	}
}

// TestValueKeepsAnExpressionsType holds that a template that is one
// expression hands back the value an operator or literal gives.
func TestValueKeepsAnExpressionsType(t *testing.T) {
	t.Parallel()
	eng := render.New()
	for source, want := range map[string]any{
		"{{ vars.n > 2 }}":       true,
		"{{ [1, 'a'] }}":         []any{int64(1), "a"},
		"{{ {'k': vars.n} }}":    map[string]any{"k": 3},
		"{{ vars.x or 'd' }}":    "d",
		"{{ 'p' ~ vars.n }}":     "p3",
		"{{ vars.n is number }}": true,
	} {
		tmpl, err := eng.Compile(source)
		if err != nil {
			t.Fatal(err)
		}
		got, single, err := tmpl.Value(map[string]any{"vars": map[string]any{"n": 3, "x": ""}})
		if !single || err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Value(%q) = %#v, %v, %v; want %#v", source, got, single, err, want)
		}
	}
}

// TestComparisonWorkIsBounded holds the step budget against comparisons
// and membership over large data inside a loop.
func TestComparisonWorkIsBounded(t *testing.T) {
	t.Parallel()
	big := make([]any, 5000)
	for i := range big {
		big[i] = i
	}
	tmpl, err := render.New().Compile("{% for x in l %}{% if 'none' in l or l == l %}{% endif %}{% endfor %}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.Render(map[string]any{"l": big}); !errors.Is(err, render.ErrTooLarge) {
		t.Errorf("a loop comparing a large list each pass gave %v, want ErrTooLarge", err)
	}
}
