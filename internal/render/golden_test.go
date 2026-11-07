// Package render_test: the template forms users rely on, pinned by one
// golden file.
//
// A template's output and its error text have no schema, yet runbooks,
// credential injectors and file templates depend on both. Each case below
// is compiled and rendered through the public Engine, and its source,
// options, output (or error) and reported names are written to
// testdata/golden/render.json, so a change to any of them fails until the
// file is updated on purpose.
package render_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/golden"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// renderCase is one template and how it is evaluated.
type renderCase struct {
	// name says what the case covers.
	name string
	// source is the template text.
	source string
	// opts are the block whitespace options it compiles under.
	opts render.Options
	// value evaluates it with Template.Value rather than Render.
	value bool
}

// renderResult is one case's line in the golden file.
type renderResult struct {
	Name         string   `json:"name"`
	Source       string   `json:"source"`
	TrimBlocks   bool     `json:"trim_blocks,omitempty"`
	LstripBlocks bool     `json:"lstrip_blocks,omitempty"`
	Names        []string `json:"names,omitempty"`
	Output       *string  `json:"output,omitempty"`
	Value        any      `json:"value,omitempty"`
	ValueType    string   `json:"value_type,omitempty"`
	IsValue      *bool    `json:"is_value,omitempty"`
	CompileError string   `json:"compile_error,omitempty"`
	RenderError  string   `json:"render_error,omitempty"`
}

// goldenVars are the variables every case renders against.
func goldenVars() map[string]any {
	return map[string]any{
		"name":      "web",
		"env":       "prod",
		"hosts":     []any{"a", "b", "c"},
		"server":    map[string]any{"host": "db1", "port": 22},
		"tricky":    "it's here",
		"padded":    "  Mixed Case  ",
		"path":      "a b/c",
		"empty":     []any{},
		"empty_str": "",
		"flag":      true,
		"nothing":   nil,
		"ratio":     1.5,
	}
}

// goldenCases are the cases, in the file's order.
func goldenCases() []renderCase {
	trim := render.Options{TrimBlocks: true}
	both := render.Options{TrimBlocks: true, LstripBlocks: true}
	block := "{% if flag %}\n  yes\n  {% endif %}\nend\n"
	return []renderCase{
		{name: "variable substitution", source: "hello {{ name }}"},
		{name: "a nested path and a number", source: "{{ server.host }}:{{ server.port }}"},
		{name: "a list index", source: "{{ hosts[1] }}"},
		{name: "a boolean and a float as text", source: "{{ flag }} {{ ratio }}"},
		{name: "none is not the empty string", source: "{{ nothing }}"},
		{name: "one expression keeps its list type", source: "{{ hosts }}", value: true},
		{name: "one expression keeps its number type", source: "{{ server.port }}", value: true},
		{name: "text around an expression is not a value", source: "port {{ server.port }}", value: true},
		{name: "default for a missing name", source: "{{ region | default('us-east-1') }}"},
		{name: "default ignored for a present name", source: "{{ name | default('other') }}"},
		{name: "quote with an apostrophe", source: "{{ tricky | quote }}"},
		{name: "to_json of a list", source: "{{ hosts | to_json }}"},
		{name: "join is not a filter", source: "{{ hosts | join(',') }}"},
		{name: "filters chain left to right", source: "{{ padded | trim | upper }}"},
		{name: "urlencode encodes a slash", source: "{{ path | urlencode }}"},
		{name: "b64encode", source: "{{ name | b64encode }}"},
		{name: "cli_token refuses a quote", source: "{{ tricky | cli_token }}"},
		{name: "concatenation", source: "{{ name ~ '-' ~ env }}"},
		{name: "or returns an operand", source: "{{ empty_str or 'fallback' }}"},
		{name: "is defined", source: "{{ name is defined }} {{ region is defined }}"},
		{name: "if, elif and else", source: "{% if env == 'dev' %}D{% elif env == 'prod' %}P{% else %}O{% endif %}"},
		{name: "for with loop.index", source: "{% for h in hosts %}{{ loop.index }}={{ h }} {% endfor %}"},
		{name: "for over a mapping's items, sorted", source: "{% for k, v in server.items() %}{{ k }}={{ v }};{% endfor %}"},
		{name: "for else on an empty list", source: "{% for x in empty %}x{% else %}none{% endfor %}"},
		{name: "set", source: "{% set greeting = 'hi ' ~ name %}{{ greeting }}"},
		{name: "raw and a comment", source: "{% raw %}{{ kept }}{% endraw %}a{# gone #}b"},
		{name: "minus whitespace control", source: "a\n  {%- if flag %} b {%- endif %}\nc"},
		{name: "block whitespace by default", source: block},
		{name: "trim_blocks", source: block, opts: trim},
		{name: "trim_blocks and lstrip_blocks", source: block, opts: both},
		{name: "an undefined name", source: "{{ missing }}"},
		{name: "an undefined key", source: "{{ server.missing }}"},
		{name: "a mapping has no text form", source: "{{ server }}"},
		{name: "ordering a number and a string", source: "{{ 1 < 'a' }}"},
		{name: "looping over a number", source: "{% for x in server.port %}{% endfor %}"},
		{name: "an unsupported statement", source: "{% macro m() %}{% endmacro %}"},
		{name: "arithmetic is refused", source: "{{ server.port + 1 }}"},
	}
}

// TestRenderMatchesTheGolden renders every case and compares the results
// with testdata/golden/render.json.
func TestRenderMatchesTheGolden(t *testing.T) {
	engine := render.New()
	vars := goldenVars()
	var results []renderResult
	for _, c := range goldenCases() {
		r := renderResult{Name: c.name, Source: c.source, TrimBlocks: c.opts.TrimBlocks, LstripBlocks: c.opts.LstripBlocks}
		tmpl, err := engine.CompileWith(c.source, c.opts)
		if err != nil {
			r.CompileError = err.Error()
			results = append(results, r)
			continue
		}
		r.Names = tmpl.Names()
		if c.value {
			v, ok, err := tmpl.Value(vars)
			r.IsValue = &ok
			if err != nil {
				r.RenderError = err.Error()
			} else if ok {
				r.Value, r.ValueType = v, fmt.Sprintf("%T", v)
			}
		} else if out, err := tmpl.Render(vars); err != nil {
			r.RenderError = err.Error()
		} else {
			r.Output = &out
		}
		results = append(results, r)
	}
	golden.JSON(t, filepath.Join("testdata", "golden", "render.json"), results)
}
