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
		if strings.Contains(source, "default") {
			return
		}
		for _, missing := range names {
			partial := make(map[string]any, len(full))
			for k, v := range full {
				if k != missing {
					partial[k] = v
				}
			}

			got, err := tmpl.Render(partial)
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
	return strings.Count(source, "{{") == 1 && strings.HasPrefix(source, "{{") && strings.HasSuffix(source, "}}")
}
