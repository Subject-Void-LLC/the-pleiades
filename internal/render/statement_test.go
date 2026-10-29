// Package render_test: each statement and its edge cases, render-time
// errors, refusals, the bounds, and what Expressions and Names report.
package render_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestStatements renders each statement and its edge cases through the
// public port, with the output Jinja2 gives for the same template and
// variables.
func TestStatements(t *testing.T) {
	t.Parallel()

	hosts := []any{"a", "b", "c"}
	tests := []struct {
		name   string
		source string
		vars   map[string]any
		want   string
	}{
		{"if true", "{% if x %}yes{% endif %}", map[string]any{"x": true}, "yes"},
		{"if false", "{% if x %}yes{% endif %}", map[string]any{"x": false}, ""},
		{"else", "{% if x %}yes{% else %}no{% endif %}", map[string]any{"x": 0}, "no"},
		{"elif", "{% if a %}A{% elif b %}B{% else %}C{% endif %}", map[string]any{"a": "", "b": 1}, "B"},
		{"elif falls to else", "{% if a %}A{% elif b %}B{% else %}C{% endif %}", map[string]any{"a": nil, "b": []any{}}, "C"},
		{"not", "{% if not x %}empty{% endif %}", map[string]any{"x": map[string]any{}}, "empty"},
		{"not not", "{% if not not x %}set{% endif %}", map[string]any{"x": "v"}, "set"},
		{"a name starting with not is a name", "{% if nothing %}y{% endif %}", map[string]any{"nothing": 1}, "y"},
		{"truthiness of a float zero", "{% if x %}y{% else %}n{% endif %}", map[string]any{"x": 0.0}, "n"},
		{"truthiness of a non-empty list", "{% if x %}y{% endif %}", map[string]any{"x": []string{"a"}}, "y"},
		{"default makes a condition optional", "{% if tls | default('') %}tls{% else %}plain{% endif %}", map[string]any{}, "plain"},
		{"a condition with a filter", "{% if x | trim %}y{% else %}n{% endif %}", map[string]any{"x": "  "}, "n"},
		{"for over a list", "{% for h in hosts %}{{ h }},{% endfor %}", map[string]any{"hosts": hosts}, "a,b,c,"},
		{"loop counters", "{% for h in hosts %}{{ loop.index }}{{ loop.index0 }}{{ loop.revindex }}{{ loop.length }}{% if loop.first %}F{% endif %}{% if loop.last %}L{% endif %} {% endfor %}",
			map[string]any{"hosts": hosts}, "1033F 2123 3213L "},
		{"for else on an empty list", "{% for h in hosts %}{{ h }}{% else %}none{% endfor %}", map[string]any{"hosts": []any{}}, "none"},
		{"for over a mapping walks sorted keys", "{% for k in m %}{{ k }}{% endfor %}", map[string]any{"m": map[string]any{"b": 1, "a": 2}}, "ab"},
		{"items", "{% for k, v in m.items() %}{{ k }}={{ v }};{% endfor %}", map[string]any{"m": map[string]any{"b": 1, "a": "x"}}, "a=x;b=1;"},
		{"keys and values", "{% for k in m.keys() %}{{ k }}{% endfor %}/{% for v in m.values() %}{{ v }}{% endfor %}", map[string]any{"m": map[string]string{"y": "2", "x": "1"}}, "xy/12"},
		{"two names unpack pairs", "{% for a, b in pairs %}{{ a }}{{ b }}{% endfor %}", map[string]any{"pairs": []any{[]any{"1", "2"}, []string{"3", "4"}}}, "1234"},
		{"for over text walks characters", "{% for c in s %}[{{ c }}]{% endfor %}", map[string]any{"s": "hé"}, "[h][é]"},
		{"nested loops and the inner loop variable", "{% for a in xs %}{% for b in xs %}{{ loop.index }}{% endfor %}|{% endfor %}", map[string]any{"xs": []any{1, 2}}, "12|12|"},
		{"a loop name hides a variable", "{% for x in xs %}{{ x }}{% endfor %}{{ x }}", map[string]any{"xs": []any{"in"}, "x": "out"}, "inout"},
		{"set", "{% set r = region | upper %}{{ r }}", map[string]any{"region": "east"}, "EAST"},
		{"set inside a for does not leak", "{% set n = a %}{% for x in xs %}{% set n = x %}{{ n }}{% endfor %}{{ n }}", map[string]any{"a": "A", "xs": []any{"1", "2"}}, "12A"},
		{"set inside an if is seen after it", "{% if c %}{% set n = a %}{% endif %}{{ n }}", map[string]any{"c": true, "a": "A", "n": "N"}, "A"},
		{"an if not taken leaves the variable", "{% if c %}{% set n = a %}{% endif %}{{ n }}", map[string]any{"c": false, "a": "A", "n": "N"}, "N"},
		{"a read before set reads the variable", "{{ n }}{% set n = a %}{{ n }}", map[string]any{"a": "A", "n": "N"}, "NA"},
		{"set keeps a list's type", "{% set l = xs %}{% for x in l %}{{ x }}{% endfor %}", map[string]any{"xs": []any{1, 2}}, "12"},
		{"comment", "a{# not {{ shown }} #}b", nil, "ab"},
		{"whitespace control on statements", "a  {%- if x -%}  b  {%- endif -%}  c", map[string]any{"x": true}, "abc"},
		{"whitespace control on comments", "a {#- c -#} b", nil, "ab"},
		{"a line-per-item file", "{% for h in hosts -%}\nserver {{ h }}\n{% endfor -%}\nend", map[string]any{"hosts": []any{"a", "b"}}, "server a\nserver b\nend"},
		{"raw", "{% raw %}{{ x }} {% if %}{% endraw %}", nil, "{{ x }} {% if %}"},
		{"raw with trim markers", "a {%- raw -%} {{ x }} {%- endraw -%} b", nil, "a{{ x }}b"},
	}

	eng := render.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := eng.Compile(tt.source)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tt.source, err)
			}
			got, err := tmpl.Render(tt.vars)
			if err != nil {
				t.Fatalf("Render(%q): %v", tt.source, err)
			}
			if got != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

// docsExample is the template docs/04-writing-runbooks.md shows, as YAML's
// literal block hands it to the renderer; file.template reads it with
// trim_blocks on (TestBlockOptions).
const docsExample = "upstream app {\n" +
	"{% for name in vars.backends %}\n" +
	"  server {{ name }}:8080;\n" +
	"{% endfor %}\n" +
	"}\n" +
	"{% if vars.tls %}\n" +
	"listen 443 ssl;\n" +
	"{% else %}\n" +
	"listen 80;\n" +
	"{% endif %}\n"

// TestStatementRenderErrors covers what a statement refuses at render
// time: strict undefined holds inside statements, and a value with no
// items cannot be looped over.
func TestStatementRenderErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		vars    map[string]any
		wantErr error
	}{
		{"an undefined condition", "{% if x %}y{% endif %}", nil, render.ErrUndefined},
		{"an undefined list", "{% for x in xs %}{% endfor %}", nil, render.ErrUndefined},
		{"an undefined set value", "{% set a = b %}", nil, render.ErrUndefined},
		{"an undefined name in a branch taken", "{% if c %}{{ missing }}{% endif %}", map[string]any{"c": 1}, render.ErrUndefined},
		{"a set name does not outlive its loop", "{% for x in xs %}{% set n = x %}{% endfor %}{{ n }}", map[string]any{"xs": []any{1}}, render.ErrUndefined},
		{"an unknown loop attribute", "{% for x in xs %}{{ loop.cycle }}{% endfor %}", map[string]any{"xs": []any{1}}, render.ErrUndefined},
		{"a number has no items", "{% for x in n %}{% endfor %}", map[string]any{"n": 3}, render.ErrNotIterable},
		{"null has no items", "{% for x in n %}{% endfor %}", map[string]any{"n": nil}, render.ErrNotIterable},
		{"items needs a mapping", "{% for k, v in l.items() %}{% endfor %}", map[string]any{"l": []any{1}}, render.ErrNotIterable},
		{"two names need pairs", "{% for a, b in l %}{% endfor %}", map[string]any{"l": []any{"x"}}, render.ErrNotIterable},
	}
	eng := render.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := eng.Compile(tt.source)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tt.source, err)
			}
			got, err := tmpl.Render(tt.vars)
			if !errors.Is(err, tt.wantErr) || got != "" {
				t.Errorf("Render(%q) = %q, %v; want \"\" and %v", tt.source, got, err, tt.wantErr)
			}
		})
	}
}

// TestStatementRefusals covers every statement and form refused at compile
// time, each naming what it refuses.
func TestStatementRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		wantErr error
		mention string
	}{
		{"macro", "{% macro m() %}{% endmacro %}", render.ErrSyntax, "macro"},
		{"include", "{% include 'x.j2' %}", render.ErrSyntax, "include"},
		{"import", "{% import 'x.j2' as x %}", render.ErrSyntax, "import"},
		{"from", "{% from 'x' import y %}", render.ErrSyntax, "from"},
		{"extends", "{% extends 'base.j2' %}", render.ErrSyntax, "extends"},
		{"block", "{% block b %}{% endblock %}", render.ErrSyntax, "block"},
		{"call", "{% call m() %}{% endcall %}", render.ErrSyntax, "call"},
		{"filter block", "{% filter upper %}x{% endfilter %}", render.ErrSyntax, "filter"},
		{"with", "{% with a = b %}{% endwith %}", render.ErrSyntax, "with"},
		{"break", "{% for x in y %}{% break %}{% endfor %}", render.ErrSyntax, "break"},
		{"an unknown statement", "{% frobnicate %}", render.ErrSyntax, "frobnicate"},
		{"an empty statement", "{% %}", render.ErrSyntax, ""},
		{"a block set", "{% set x %}v{% endset %}", render.ErrSyntax, "set block"},
		{"set with two names", "{% set a, b = c %}", render.ErrSyntax, "one name"},
		{"set of an attribute", "{% set ns.a = c %}", render.ErrSyntax, "attribute"},
		{"an inline if on a for", "{% for x in y if x %}{% endfor %}", render.ErrSyntax, "inline if"},
		{"a recursive for", "{% for x in y recursive %}{% endfor %}", render.ErrSyntax, "recursive"},
		{"loop as a loop name", "{% for loop in y %}{% endfor %}", render.ErrSyntax, "loop"},
		{"three loop names", "{% for a, b, c in y %}{% endfor %}", render.ErrSyntax, "two"},
		{"for without in", "{% for x y %}{% endfor %}", render.ErrSyntax, "in"},
		{"items after a filter", "{% for k, v in m | lower.items() %}{% endfor %}", render.ErrSyntax, ""},
		{"set of a keyword", "{% set none = a %}", render.ErrSyntax, "keyword"},
		{"not with nothing after it", "{% if not %}{% endif %}", render.ErrSyntax, "not"},
		{"an unclosed if", "{% if a %}x", render.ErrSyntax, "endif"},
		{"an unclosed for", "{% for a in b %}x", render.ErrSyntax, "endfor"},
		{"endfor closing an if", "{% if a %}{% endfor %}", render.ErrSyntax, "endfor"},
		{"elif after else", "{% if a %}{% else %}{% elif b %}{% endif %}", render.ErrSyntax, "elif"},
		{"a second else", "{% if a %}{% else %}{% else %}{% endif %}", render.ErrSyntax, "else"},
		{"else outside a block", "{% else %}", render.ErrSyntax, "else"},
		{"endif with text after it", "{% if a %}{% endif a %}", render.ErrSyntax, "endif"},
		{"an expression inside a statement", "{% if {{ a }} %}{% endif %}", render.ErrSyntax, ""},
		{"an unterminated statement", "{% if a", render.ErrSyntax, ""},
		{"an unterminated comment", "{# never closed", render.ErrSyntax, "comment"},
		{"raw with no endraw", "{% raw %}{{ x }}", render.ErrSyntax, "endraw"},
		{"endraw with no raw", "{% endraw %}", render.ErrSyntax, "endraw"},
		{"an unknown filter in a condition", "{% if a | lookup('file') %}{% endif %}", render.ErrUnknownFilter, ""},
		{"nesting past the bound", strings.Repeat("{% if a %}", 17) + strings.Repeat("{% endif %}", 17), render.ErrTooLarge, ""},
		{"a name set from too many values", manyOrigins(17), render.ErrTooLarge, ""},
	}
	eng := render.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := eng.Compile(tt.source)
			if err == nil || tmpl != nil {
				t.Fatalf("Compile(%q) = %v, %v; want only an error", tt.source, tmpl, err)
			}
			if !errors.Is(err, tt.wantErr) || !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("Compile(%q) error %v, want %v naming %q", tt.source, err, tt.wantErr, tt.mention)
			}
		})
	}

	// Each limit's boundary compiles, so a change cannot move the refusal.
	for _, ok := range []string{strings.Repeat("{% if a %}", 16) + strings.Repeat("{% endif %}", 16), manyOrigins(16)} {
		if _, err := eng.Compile(ok); err != nil {
			t.Errorf("a template at the limit failed to compile: %v", err)
		}
	}
}

// manyOrigins is a template setting n from k different roots, one per if.
func manyOrigins(k int) string {
	var b strings.Builder
	for i := range k {
		b.WriteString("{% if c %}{% set n = r" + string(rune('a'+i)) + " %}{% endif %}")
	}
	return b.String() + "{{ n }}"
}

// TestStatementBounds holds the render-time bounds: total loop iterations
// across nested loops, evaluation steps, and output size inside a loop.
func TestStatementBounds(t *testing.T) {
	t.Parallel()
	eng := render.New()
	list := func(n int) []any { return make([]any, n) }

	cases := []struct {
		name   string
		source string
		vars   map[string]any
		ok     bool
	}{
		{"10,000 iterations is the bound", "{% for x in xs %}{% endfor %}", map[string]any{"xs": list(10000)}, true},
		{"10,001 is past it", "{% for x in xs %}{% endfor %}", map[string]any{"xs": list(10001)}, false},
		{"nested loops count together", "{% for a in xs %}{% for b in xs %}{% endfor %}{% endfor %}", map[string]any{"xs": list(101)}, false},
		{"100 by 100 nested fits", "{% for a in xs %}{% for b in xs %}{% endfor %}{% endfor %}", map[string]any{"xs": list(99)}, true},
		{"a long text is refused before it is split", "{% for c in s %}{% endfor %}", map[string]any{"s": strings.Repeat("x", 1<<20)}, false},
		{"a body that writes nothing still counts steps", "{% for x in xs %}" + strings.Repeat("{% if c %}{% endif %}", 200) + "{% endfor %}", map[string]any{"xs": list(10000), "c": false}, false},
		{"output inside a loop is bounded", "{% for x in xs %}{{ big }}{% endfor %}", map[string]any{"xs": list(100), "big": strings.Repeat("y", 20000)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := eng.Compile(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			out, err := tmpl.Render(tc.vars)
			if tc.ok && err != nil {
				t.Errorf("Render failed: %v", err)
			}
			if !tc.ok && (!errors.Is(err, render.ErrTooLarge) || out != "") {
				t.Errorf("Render = %d bytes, %v; want ErrTooLarge and nothing", len(out), err)
			}
		})
	}
}

// TestStatementExpressionsAndNames holds what callers' root rules read:
// every expression inside a statement is reported with its kind, a read
// through a bound name is reported as a read of what it was bound from,
// loop counters are not roots, and Names never lists a bound name.
func TestStatementExpressionsAndNames(t *testing.T) {
	t.Parallel()
	type info = render.ExpressionInfo
	out, cond, iter, assign := render.ExpressionOutput, render.ExpressionCondition, render.ExpressionIterable, render.ExpressionAssignment
	tests := []struct {
		name   string
		source string
		want   []info
		names  []string
	}{
		{"a condition and a list are reported",
			"{% if vars.on %}{% for h in nodes.r.hosts %}{{ h | quote }}{{ loop.index }}{% endfor %}{% endif %}",
			[]info{{Path: []string{"vars", "on"}, Kind: cond}, {Path: []string{"nodes", "r", "hosts"}, Kind: iter}, {Path: []string{"nodes", "r", "hosts"}, Filters: []string{"quote"}, Kind: out}},
			[]string{"nodes", "vars"}},
		{"a set name reads its value's root",
			"{% set c = vars.cmd | quote %}{{ c }}",
			[]info{{Path: []string{"vars", "cmd"}, Filters: []string{"quote"}, Kind: assign}, {Path: []string{"vars", "cmd"}, Kind: out}},
			[]string{"vars"}},
		{"a name shadowing a root reads what it was bound from",
			"{% set vars = nodes.x %}{{ vars.y }}",
			[]info{{Path: []string{"nodes", "x"}, Kind: assign}, {Path: []string{"nodes", "x"}, Kind: out}},
			[]string{"nodes"}},
		{"an optional binding stays optional",
			"{% for h in vars.hosts | default('') %}{{ h }}{% endfor %}",
			[]info{{Path: []string{"vars", "hosts"}, Filters: []string{"default"}, Kind: iter, Guarded: true}, {Path: []string{"vars", "hosts"}, Filters: []string{"default"}, Kind: out, Guarded: true}},
			[]string{"vars"}},
		{"a name maybe set reports both readings",
			"{% if c %}{% set n = vars.a %}{% endif %}{{ n }}",
			[]info{{Path: []string{"c"}, Kind: cond}, {Path: []string{"vars", "a"}, Kind: assign}, {Path: []string{"n"}, Kind: out}, {Path: []string{"vars", "a"}, Kind: out}},
			[]string{"c", "n", "vars"}},
		{"a read before the set reads the variable",
			"{{ n }}{% set n = vars.a %}",
			[]info{{Path: []string{"n"}, Kind: out}, {Path: []string{"vars", "a"}, Kind: assign}},
			[]string{"n", "vars"}},
		{"the loop variable outside a loop is the caller's",
			"{{ loop.index }}{% for x in xs %}{% endfor %}",
			[]info{{Path: []string{"loop", "index"}, Kind: out}, {Path: []string{"xs"}, Kind: iter}},
			[]string{"loop", "xs"}},
		{"a pair's names read the mapping",
			"{% for k, v in vars.m.items() %}{{ k }}{{ v }}{% endfor %}",
			[]info{{Path: []string{"vars", "m"}, Kind: iter}, {Path: []string{"vars", "m"}, Kind: out}, {Path: []string{"vars", "m"}, Kind: out}},
			[]string{"vars"}},
	}
	eng := render.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := eng.Compile(tt.source)
			if err != nil {
				t.Fatal(err)
			}
			if got := tmpl.Expressions(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Expressions() = %#v\nwant %#v", got, tt.want)
			}
			if got := tmpl.Names(); !reflect.DeepEqual(got, tt.names) {
				t.Errorf("Names() = %v, want %v", got, tt.names)
			}
		})
	}
}

// TestValueIsTextOnceAStatementOrCommentAppears holds that Value evaluates
// only a template that is one expression and nothing else.
func TestValueIsTextOnceAStatementOrCommentAppears(t *testing.T) {
	t.Parallel()
	eng := render.New()
	for _, source := range []string{"{{ x }}{# note #}", "{% set y = x %}{{ y }}", "{% if x %}{{ x }}{% endif %}"} {
		tmpl, err := eng.Compile(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, single, _ := tmpl.Value(map[string]any{"x": []any{1}}); single {
			t.Errorf("Value(%q) evaluated a template that is not one expression", source)
		}
	}
}
