// Package render_test: each operator over every Go value shape a caller
// hands in, the remaining refusals, and the syntax lists pleiades doc prints.
package render_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestOperatorsOverEveryValueShape runs the operators and tests over the
// Go shapes a caller's data arrives in, not only the decoded-JSON ones: a
// typed slice, a string map, sized integers and floats, so Python's rules
// hold whatever the caller built.
func TestOperatorsOverEveryValueShape(t *testing.T) {
	t.Parallel()
	vars := map[string]any{
		"ss": []string{"a", "b"}, "sm": map[string]string{"k": "v"}, "ints": []int{1, 2},
		"i32": int32(3), "u8": uint8(3), "f32": float32(0.5), "arr": [2]string{"x", "y"},
		"m1": map[string]any{"a": []any{1}}, "m2": map[string]any{"a": []any{1}}, "m3": map[string]any{"b": 1},
		"m4": map[string]any{"a": []any{2}}, "im": map[int]string{1: "x"}, "zero": uint(0), "ptr": (*int)(nil),
		"st": struct{}{}, "nested": [][]any{{1}},
	}
	tests := []struct{ source, want string }{
		{"{{ ss == ['a', 'b'] }} {{ ss == ['a'] }} {{ ss == ['a', 'c'] }} {{ 'b' in ss }}", "true false false true"},
		{"{{ sm == {'k': 'v'} }} {{ 'k' in sm }} {{ sm == {'z': 'v'} }} {{ sm == {} }}", "true true false false"},
		{"{{ m1 == m2 }} {{ m1 == m3 }} {{ m1 == m4 }} {{ m1 == 'x' }} {{ none == 0 }}", "true false false false false"},
		{"{{ ints == [1, 2] }} {{ arr[1] }} {{ 'x' in arr }} {{ nested[0][0] }}", "true y true 1"},
		{"{{ i32 == 3 }} {{ u8 == 3.0 }} {{ f32 < 1 }} {{ i32 > u8 }} {{ i32 is number }} {{ f32 is number }}", "true true true false true true"},
		{"{{ [1, 'a'] < [1, 'b'] }} {{ [1] <= [1] }} {{ [2] > [1, 5] }} {{ 'b' >= 'a' }}", "true true true true"},
		{"{{ ss is iterable }} {{ sm is mapping }} {{ sm is iterable }} {{ ss is sameas none }} {{ true is sameas 1 }}", "true true true false false"},
		{"{% if zero %}y{% else %}n{% endif %}{% if f32 %}y{% endif %}{% if ptr %}y{% else %}n{% endif %}{% if st %}y{% endif %}{% if arr %}y{% endif %}", "nyny" + "y"},
		{"{% for x in ss %}{{ x }}{% endfor %}{% for x in arr %}{{ x }}{% endfor %}{% for x in ints %}{{ x }}{% endfor %}{% for k in sm %}{{ k }}{% endfor %}", "abxy12k"},
		{"{% for k, v in sm.items() %}{{ k }}{{ v }}{% endfor %}{% for v in sm.values() %}{{ v }}{% endfor %}", "kvv"},
		{"{{ 'a' ~ 1 ~ 2.5 ~ true }}", "a12.5true"},
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

	// Errors a value's shape causes when it runs.
	for source, wantErr := range map[string]error{
		"{% for x in im %}{% endfor %}":      render.ErrNotIterable,
		"{% for x in st %}{% endfor %}":      render.ErrNotIterable,
		"{{ im.items() }}":                   render.ErrNotIterable,
		"{{ [1, missing] }}":                 render.ErrUndefined,
		"{{ {'a': missing} }}":               render.ErrUndefined,
		"{{ missing.items() }}":              render.ErrUndefined,
		"{{ not missing }}":                  render.ErrUndefined,
		"{{ missing ~ 'x' }}":                render.ErrUndefined,
		"{{ missing | upper }}":              render.ErrUndefined,
		"{{ missing | upper | default(1) }}": render.ErrUndefined,
		"{{ missing is none }}":              render.ErrUndefined,
		"{{ (1 ~ missing) is defined }}":     render.ErrUndefined,
		"{{ 1 is sameas missing }}":          render.ErrUndefined,
		"{{ [st] < [st] }}":                  render.ErrNotComparable,
		"{{ [1] < ['a'] }}":                  render.ErrNotComparable,
		"{{ st < st }}":                      render.ErrNotComparable,
		"{{ 1 in none }}":                    render.ErrNotIterable,
		"{{ [1] in sm }}":                    render.ErrNotComparable,
		"{{ m1 < 1 }}":                       render.ErrNotComparable,
		"{{ missing < 1 }}":                  render.ErrUndefined,
		"{{ 1 < missing }}":                  render.ErrUndefined,
	} {
		tmpl, err := eng.Compile(source)
		if err != nil {
			t.Errorf("Compile(%q): %v", source, err)
			continue
		}
		if _, err := tmpl.Render(vars); !errors.Is(err, wantErr) {
			t.Errorf("Render(%q) error %v, want %v", source, err, wantErr)
		}
	}

	// A defined test over something that is not a path is true once it
	// evaluates, as Jinja2's is.
	tmpl, err := eng.Compile("{{ (1 ~ 'x') is defined }} {{ ss[0] | upper is defined }}")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tmpl.Render(vars); err != nil || got != "true true" {
		t.Errorf("defined over an expression = %q, %v", got, err)
	}
}

// TestMoreCompileRefusals covers the remaining forms the parser refuses.
func TestMoreCompileRefusals(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"{{ a[ }}", "{{ a['k' }}", "{{ a[x] }}", "{{ a.1 }}", "{{ (a }}", "{{ (a b) }}", "{{ (a, b }}",
		"{{ {'a' 1} }}", "{{ {'a': 1 'b': 2} }}", "{{ {'a': } }}", "{{ {'a } }}", "{{ [1 2] }}", "{{ [1, }}",
		"{{ a | default( }}", "{{ a | default(1, }}", "{{ a | upper( }}", "{{ a is }}", "{{ a is not }}",
		"{{ a is sameas( }}", "{{ a is 1 }}", "{{ a | }}", "{{ a | 1 }}", "{{ a.items(1) }}", "{{ a() }}",
		"{{ a ~ }}", "{{ a or }}", "{{ a and }}", "{{ a not b }}", "{{ 'a' 'b' }}", "{{ 1.x }}", "{{ 1. }}",
		"{{ 0b2 }}", "{{ 0x }}", "{{ 1_ }}", "{{ '" + bs + "u12' }}", "{{ '" + bs + "U0011FFFF' }}", "{{ '" + bs,
		"{% set x == 1 %}", "{% set %}", "{% set 1 = a %}", "{% set x = %}", "{% for %}{% endfor %}",
		"{% for x, %}{% endfor %}", "{% for x in %}{% endfor %}", "{% for true in a %}{% endfor %}",
		"{% for a, a in b %}{% endfor %}", "{% elif a %}", "{% if a %}{% elif %}{% endif %}", "{% 1 %}",
		strings.Repeat("{{ a ", 1) + "| " + strings.Repeat("upper | ", 9) + "upper }}",
		"{{ a " + strings.Repeat("is string ", 9) + "}}",
		"{{ (" + strings.Repeat("1,", 257) + ") }}",
		"{{ {" + strings.Repeat("'k': 1, ", 1) + strings.Repeat("'", 0) + "} }}" + "{{ {" + manyKeys(257) + "} }}",
	} {
		if _, err := render.New().Compile(source); err == nil {
			t.Errorf("Compile(%q) succeeded, want a refusal", source)
		}
	}
}

// manyKeys is a mapping literal's body with n distinct keys.
func manyKeys(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString("'k" + strings.Repeat("x", i) + "': 1, ")
	}
	return b.String()
}

// TestSyntaxDocsNameTheGrammar holds the doc lists to the parser: every
// usage they show compiles, and every test is listed.
func TestSyntaxDocsNameTheGrammar(t *testing.T) {
	t.Parallel()
	if len(render.Statements()) == 0 || len(render.Operators()) == 0 || len(render.NotSupported()) == 0 {
		t.Fatal("a syntax list is empty")
	}
	eng := render.New()
	tests := render.Tests()
	if len(tests) != 7 {
		t.Errorf("Tests() lists %d tests, want 7", len(tests))
	}
	for _, d := range tests {
		if _, err := eng.Compile("{{ " + d.Usage + " }}"); err != nil {
			t.Errorf("test %s's usage %q does not compile: %v", d.Name, d.Usage, err)
		}
	}
	for _, d := range render.Statements() {
		if _, err := eng.Compile(d.Usage); err != nil && d.Name != "trim" && d.Name != "keep" {
			t.Errorf("statement %s's usage %q does not compile: %v", d.Name, d.Usage, err)
		}
	}
}
