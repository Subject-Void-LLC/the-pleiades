package render_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// maxOutputBytesUnderTest mirrors maxOutputBytes in limits.go. The constant
// is unexported on purpose, so this test states the number it depends on
// and fails loudly if the two ever disagree.
const maxOutputBytesUnderTest = 1 << 20

// FuzzCompileAndRender is the Phase 22 checklist's fuzz gate for the
// renderer.
//
// It is not only a crash hunt. Three of the five properties below are
// correctness statements a table of hand-written cases cannot establish,
// because they have to hold for every input rather than for the inputs
// somebody thought of. The third one in particular is the reason this
// package rejects Jinja2's default behavior, and it is the property most
// worth having a fuzzer behind.
func FuzzCompileAndRender(f *testing.F) {
	seeds := []string{
		// The real injector templates from the committed parity corpus,
		// tests/parity/testdata/credential_types/custom-rest-api-token.json.
		"{{ api_token }}",
		"{{ api_url }}",

		// Shapes the managed AWX types use.
		"{{ tower.filename }}",
		"{{ tower.filename.cert }}",
		"{{ username }}:{{ password }}",
		"{{ region | default('us-east-1') }}",
		"{{ cfg | to_json }}",
		"{{ key | b64encode }}",
		"{{ pw | quote }}",
		"[default]\naws_access_key_id={{ id }}\naws_secret_access_key={{ secret }}\n",

		// Whitespace control, which AWX exports contain.
		"{{- x -}}",

		// Hostile and malformed input.
		"",
		"{{",
		"}}",
		"{{}}",
		"{{ }}",
		"{{ x |",
		"{{ x | }}",
		"{%",
		"{% for x in y %}{{ x }}{% endfor %}",
		"{# comment #}",
		"{{ a {{ b }}",
		"{{ a | default('",
		`{{ a | default("unterminated }}`,
		`{{ a | default('\q') }}`,
		"{{ a[ }}",
		"{{ a[-] }}",
		"{{ a[99999999999999999999] }}",
		"{{ 1 }}",
		"{{ .a }}",
		"{{ a..b }}",
		"{{ a.b.c.d.e.f.g.h.i.j.k.l.m.n.o.p.q.r.s.t }}",
		strings.Repeat("{{", 1000),
		strings.Repeat("{{ a }}", 1000),
		"{{ a }}\x00{{ b }}",
		"{{ \xff\xfe }}",
		"{{ a | " + strings.Repeat("upper | ", 50) + "upper }}",

		// Phase 117a's rendered task parameters: the new filters, a URL
		// built around an expression, a value that is one expression and
		// nothing else, and a path with an index.
		"{{ ticket | urlencode }}",
		"{{ ci | cli_token }}",
		"/api/now/table/incident/{{ result.ticket.json.sys_id | urlencode }}",
		"{{ result.collect.stdout | to_json }}",
		`{"body": {{ result.collect.stdout | to_json }}}`,
		"{{ nodes.x.y[0].z }}",
		"{{ a | urlencode | cli_token }}",
		"show interface {{ ci | cli_token }}",

		// Loop-item shaped input (Phases 88 and 89): an item, a field of it,
		// the loop counters, nested braces, and a variable that reads itself.
		"{{ item }}",
		"{{ item.name }}-{{ loop.index }}/{{ loop.length }}",
		"{{ item.a.b.c.d }}",
		"{{ item[0] }}",
		"{{ item.name | default('none') | quote }}",
		"{{ vars.selfish }}",
		"{{ {{ item }} }}",
		"{{ item.{{ item }} }}",
		strings.Repeat("{{ item.", 200) + "x" + strings.Repeat(" }}", 200),
		strings.Repeat("{{ item }},", 5000),
		"{{ item | to_json }}{{ loop.first }}{{ loop.last }}{{ loop.index0 }}",

		// Statements: each one, its edge cases, refused ones, whitespace
		// control, nesting near the bound, and loops whose work the bounds
		// must stop.
		"{% if a %}x{% elif b %}y{% else %}z{% endif %}",
		"{% if not a %}{{ b }}{% endif %}",
		"{% if a %}{% else %}{{ b }}{% endif %}",
		"{% for x in a %}{{ x }}{{ loop.index }}/{{ loop.length }}{% else %}{{ b }}{% endfor %}",
		"{% for k, v in a.items() %}{{ k }}={{ v }}{% endfor %}",
		"{% for k in a.keys() %}{% endfor %}{% for v in a.values() %}{{ v }}{% endfor %}",
		"{% set n = a | upper %}{% for x in b %}{% set n = x %}{{ n }}{% endfor %}{{ n }}",
		"{% if c %}{% set n = a %}{% endif %}{{ n }}",
		"{{ n }}{% set n = a %}{{ n }}",
		"{% for a in a %}{% for a in a %}{{ a }}{% endfor %}{% endfor %}",
		"a {%- if b -%} c {%- endif -%} d {#- e -#} f",
		"{% raw %}{{ x }}{% endraw %}",
		"{% raw -%} {% if %} {%- endraw %}",
		"{% if %}",
		"{% endif %}",
		"{% if a %}",
		"{% else %}",
		"{% for loop in a %}{% endfor %}",
		"{% for a, b, c in d %}{% endfor %}",
		"{% for x in y if z %}{% endfor %}",
		"{% set a %}{% endset %}",
		"{% macro m() %}{% endmacro %}",
		"{% include 'x' %}",
		"{% if {{ a }} %}{% endif %}",
		"{# {{ a }} {% if %} #}",
		"{#",
		"{% raw %}",
		strings.Repeat("{% if a %}", 17) + strings.Repeat("{% endif %}", 17),
		strings.Repeat("{% for x in a %}", 16) + "{{ x }}" + strings.Repeat("{% endfor %}", 16),
		strings.Repeat("{% if c %}{% set n = a %}{% endif %}", 20) + "{{ n }}",
		strings.Repeat("{% raw %}{%", 2000),

		// Expressions: literals, every operator, tests, nesting near the
		// bounds, and forms refused.
		"{% if vars.env == 'prod' %}p{% elif n >= 2 and not off %}m{% endif %}",
		"{{ a == 'x' }}{{ a != 1 }}{{ a < 'b' <= c }}{{ 1 < 2.5 }}{{ [1, 2] < [1, 3] }}",
		"{{ a in ['x', 'y'] }}{{ 'k' not in m }}{{ 'ub' in a }}{{ a in (1,) }}",
		"{{ a or b }}{{ a and b }}{{ not a }}{{ (a or b) and c }}",
		"{{ a is defined }}{{ a is not defined }}{{ a is none }}{{ a is string }}{{ a is number }}",
		"{{ a is mapping }}{{ a is iterable }}{{ a is sameas false }}{{ a is sameas(none) }}",
		"{{ a ~ '-' ~ b | upper }}{{ (a ~ b) | quote }}",
		`{{ [a, {'k': b, "j": [1, 2.5, -3, 0x1f, true, None]}] | to_json }}`,
		"{{ 'esc\\x41\\101\\n\\'' }}",
		"{{ m.items() | to_json }}{% for k, v in m.items() %}{{ k }}{% endfor %}",
		"{{ a | default(b) }}{{ a | default(b ~ 'x') | quote }}",
		"{% set on = a == 'x' %}{{ on }}{% set l = [a, b] %}{% for x in l %}{{ x }}{% endfor %}",
		"{{ {'a': {'b': {'c': 1}}} | to_json }}",
		"{{ '}}' }}{% set s = '%}' %}{{ s }}",
		"{{ a + 1 }}", "{{ -a }}", "{{ a if b else c }}", "{{ a[b] }}", "{{ a.upper() }}", "{{ a is even }}",
		"{{ 012 }}", "{{ 1e999 }}", "{{ [1, 2 }}", "{{ {a: 1} }}", "{{ and }}", "{{ a == }}",
		"{{ " + strings.Repeat("(", 33) + "a" + strings.Repeat(")", 33) + " }}",
		"{{ " + strings.Repeat("not ", 40) + "a }}",
		"{{ [" + strings.Repeat("a,", 300) + "] }}",
		"{{ " + strings.Repeat("a ~ ", 500) + "a }}",
		"{{ " + strings.Repeat("[", 40) + strings.Repeat("]", 40) + " }}",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	eng := render.New()

	f.Fuzz(func(t *testing.T, source string) {
		// Property 1: Compile never panics, for any input. The f.Fuzz
		// harness itself is the assertion.
		tmpl, err := eng.Compile(source)

		// Property 2: Compile returns exactly one of a Template and an
		// error. Returning both, or neither, would leave a caller with no
		// correct way to branch.
		if (tmpl == nil) == (err == nil) {
			t.Fatalf("Compile(%q) returned template=%v err=%v, want exactly one of the two", source, tmpl != nil, err)
		}
		if err != nil {
			return
		}

		names := tmpl.Names()

		// Render against a complete variable map. Every name gets a
		// distinct, non-empty sentinel.
		full := make(map[string]any, len(names))
		for _, n := range names {
			full[n] = "sentinel-" + n
		}

		out, err := tmpl.Render(full)
		if err != nil {
			// A complete map can still fail legitimately: a path descending
			// into a string sentinel is undefined, a filter can reject a
			// value. Neither is a violation of anything asserted here.
			return
		}

		// Property 4: output stays inside the documented bound.
		if len(out) > maxOutputBytesUnderTest {
			t.Fatalf("Render() produced %d bytes, past the %d limit", len(out), maxOutputBytesUnderTest)
		}

		// Property 5: cache coherence. Compiling the same source again must
		// produce a template that renders identically, whether the second
		// call hit the cache or compiled fresh past the cache bound.
		again, err := eng.Compile(source)
		if err != nil {
			t.Fatalf("Compile(%q) succeeded then failed on a second call: %v", source, err)
		}
		outAgain, err := again.Render(full)
		if err != nil {
			t.Fatalf("a recompiled template failed to render input that worked the first time: %v", err)
		}
		if out != outAgain {
			t.Fatalf("recompiling changed the output: %q then %q", out, outAgain)
		}

		// Property 6 (Phase 117a): Expressions agrees with Names about which
		// roots a template reads, every path starts with its root, and every
		// filter an expression names is one Compile accepted.
		roots := map[string]bool{}
		for _, e := range tmpl.Expressions() {
			if len(e.Path) == 0 {
				t.Fatalf("Expressions() reported an empty path for %q", source)
			}
			roots[e.Path[0]] = true
		}
		if len(roots) != len(names) {
			t.Fatalf("Expressions() reads roots %v and Names() reports %v", roots, names)
		}
		for _, n := range names {
			if !roots[n] {
				t.Fatalf("Names() reports %q, which no expression reads", n)
			}
		}

		// Property 7 (Phase 117a): Value evaluates exactly the templates that
		// are one expression and nothing else, and for those it agrees with
		// Render, since every sentinel and every filter's output is text.
		value, single, err := tmpl.Value(full)
		if single && !valueIsWhole(source) {
			t.Fatalf("Value(%q) evaluated a template that is not one action and nothing else", source)
		}
		if single {
			if err != nil {
				t.Fatalf("Value(%q) failed where Render succeeded: %v", source, err)
			}
			if text, isText := value.(string); isText && text != out {
				t.Fatalf("Value(%q) = %q and Render gave %q", source, text, out)
			}
		}

		// Property 3, the security property, and the reason this package
		// exists.
		//
		// A name that was not supplied must never render as the empty
		// string. If it did, an injector would set its environment
		// variable to "", the playbook would run, and the authentication
		// failure downstream would be attributed to the wrong thing.
		//
		// Stated so a fuzzer can check it: with no default filter in play,
		// removing any single name from a complete map must make Render
		// fail with ErrUndefined. Success there is a silent empty
		// substitution by definition, because there is nothing else the
		// missing value could have become.
		//
		// Templates using the default filter are excluded, since rescuing
		// an undefined name is exactly what that filter is for. The check
		// is a plain substring test rather than an inspection of the parsed
		// filters, which makes it over-broad: a template with the word
		// "default" in its literal text is skipped too. Over-broad is the
		// safe direction, because it only ever skips a case, never passes
		// a violation.
		if strings.Contains(source, "default") || strings.Contains(source, "defined") {
			return
		}
		// A statement can leave a name in a branch not taken or a loop that
		// ran no time, so with statements the property is the one beneath
		// the rule: a render without a name either fails with ErrUndefined,
		// because it read the name, or never read it and so writes exactly
		// what the complete map wrote. It never writes anything else.
		// and, or and a chained comparison ("a in b in c" too) short-circuit
		// the same way. The substring tests are over-broad, the safe
		// direction: they only ever choose the weaker, still sound, form.
		branching := strings.Contains(source, "{%") || strings.Contains(source, "and") ||
			strings.Contains(source, "or") || strings.Contains(source, "in") || strings.ContainsAny(source, "<>=!")
		for _, missing := range names {
			partial := make(map[string]any, len(full))
			for k, v := range full {
				if k != missing {
					partial[k] = v
				}
			}

			got, err := tmpl.Render(partial)
			if err == nil && branching {
				if got != out {
					t.Fatalf("Render(%q) without %q succeeded with %q, and the complete map gave %q", source, missing, got, out)
				}
				continue
			}
			if err == nil {
				t.Fatalf("Render(%q) without %q succeeded and produced %q, so a missing variable was silently substituted", source, missing, got)
			}
			if !errors.Is(err, render.ErrUndefined) {
				t.Fatalf("Render(%q) without %q failed with %v, want an error matching ErrUndefined", source, missing, err)
			}
			if got != "" {
				t.Fatalf("Render() returned %q alongside an undefined error, want the empty string", got)
			}
		}
	})
}

// valueIsWhole reports whether source could be one action and nothing
// else, the only shape Value may evaluate. It is a necessary condition, not
// a sufficient one ("{{ a }}}}" passes it and ends in text), so the fuzz
// target asserts only that Value never evaluates a source failing it.
//
// A trim marker ("{{-" or "-}}") removes the whitespace beside it, so that
// whitespace is not text the template holds: " {{- a }}" is one action and
// nothing else.
func valueIsWhole(source string) bool {
	if strings.HasPrefix(strings.TrimLeft(source, " \t\r\n"), "{{-") {
		source = strings.TrimLeft(source, " \t\r\n")
	}
	if strings.HasSuffix(strings.TrimRight(source, " \t\r\n"), "-}}") {
		source = strings.TrimRight(source, " \t\r\n")
	}
	// No count of "{{" here: a string literal may hold one ("{{ '{{' }}").
	return strings.HasPrefix(source, "{{") && strings.HasSuffix(source, "}}")
}

// TestRender_LoopItemShapes renders the shapes a loop hands a template: an
// item that is not a map, a field it lacks, nested lists, a huge list and a
// variable that names itself. Each must end in a value or an error, never a
// panic or a runaway, and a self-reading variable stays text.
func TestRender_LoopItemShapes(t *testing.T) {
	eng := render.New()
	huge := make([]any, 100000)
	for i := range huge {
		huge[i] = map[string]any{"name": "n", "id": i}
	}
	var deep any = "leaf"
	for range 500 {
		deep = []any{deep}
	}
	items := []any{nil, 3, "text", true, []any{}, map[string]any{}, map[string]any{"name": "a"}, deep, huge}
	sources := []string{
		"{{ item }}", "{{ item.name }}", "{{ item.name.more }}", "{{ item[0] }}",
		"{{ item | to_json }}", "{{ loop.index }}-{{ loop.length }}", "{{ vars.selfish }}",
	}
	for _, source := range sources {
		tmpl, err := eng.Compile(source)
		if err != nil {
			t.Fatalf("Compile(%q): %v", source, err)
		}
		for _, item := range items {
			vars := map[string]any{
				"item":    item,
				"loop":    map[string]any{"index": 1, "length": len(items)},
				"vars":    map[string]any{"selfish": "{{ vars.selfish }}"},
				"nothing": nil,
			}
			out, err := tmpl.Render(vars)
			if err == nil && len(out) > maxOutputBytesUnderTest {
				t.Errorf("Render(%q) gave %d bytes, past the %d limit", source, len(out), maxOutputBytesUnderTest)
			}
		}
	}
	tmpl, err := eng.Compile("{{ vars.selfish }}")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tmpl.Render(map[string]any{"vars": map[string]any{"selfish": "{{ vars.selfish }}"}})
	if err != nil || out != "{{ vars.selfish }}" {
		t.Errorf("a self-reading variable rendered %q, %v, want its own text back", out, err)
	}
}
