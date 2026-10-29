// Tests that a template's statements are held to the same rules as its
// {{ }} expressions: the roots a parameter may read, quoting in command
// text, vars alone in a project path, and a fixed host in a URL. A loop's
// list, a condition and a set's value are each checked, and a read through
// a name a statement bound is checked as a read of what it was bound from.
package engine

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestTemplateProblems_StatementsAreHeldToTheSameRules is the table.
func TestTemplateProblems_StatementsAreHeldToTheSameRules(t *testing.T) {
	eng := render.New()
	cases := []struct {
		name    string
		source  string
		format  collection.ParamFormat
		refused string
	}{
		{"a loop over vars, each item quoted", "{% for f in vars.files %}rm {{ f | quote }}; {% endfor %}", collection.ParamFormatCommand, ""},
		{"a condition needs no quote", "{% if vars.force %}-f {% endif %}{{ vars.path | quote }}", collection.ParamFormatCommand, ""},
		{"a loop item written unquoted", "{% for f in vars.files %}rm {{ f }}; {% endfor %}", collection.ParamFormatCommand, "must end with | quote"},
		{"a quoted list does not quote its items", "{% for c in vars.cmd | quote %}{{ c }}{% endfor %}", collection.ParamFormatCommand, "must end with | quote"},
		{"a set name written unquoted", "{% set c = vars.cmd | quote %}{{ c }}", collection.ParamFormatCommand, "must end with | quote"},
		{"a condition reading an unknown root", "{% if secret_thing %}x{% endif %}", "", `reads "secret_thing"`},
		{"a loop over an unknown root", "{% for x in env %}{{ x }}{% endfor %}", "", `reads "env"`},
		{"a set from an unknown root", "{% set x = env.HOME %}{{ x }}", "", `reads "env"`},
		{"a name shadowing vars is checked by its source", "{% set vars = nodes.r %}{{ vars.name }}", collection.ParamFormatProjectPath, "never nodes"},
		{"a project path from a device's output through a loop", "{% for n in result.r.names %}{{ n }}{% endfor %}", collection.ParamFormatProjectPath, "never result"},
		{"a project path from vars through a loop", "{% for n in vars.names %}{{ n }}{% endfor %}", collection.ParamFormatProjectPath, ""},
		{"a URL whose host a condition chooses", "https://{% if vars.a %}a{% else %}b{% endif %}.example.com/{{ vars.p | urlencode }}", collection.ParamFormatURL, "scheme and host"},
		{"a URL with a statement after its host", "https://api.example.com/{% if vars.a %}{{ vars.p | urlencode }}{% endif %}", collection.ParamFormatURL, ""},
		// A comment renders as nothing, so the "#" or "/" inside one ends no
		// host, and a comment cannot stand in for the scheme and host.
		{"a comment's own # does not end the host", "https://api.example.com{# x #}{{ vars.h }}/p", collection.ParamFormatURL, "scheme and host"},
		{"a comment's own URL fixes nothing", "{# https://x/ #}{{ vars.url }}", collection.ParamFormatURL, "scheme and host"},
		{"a comment after the host's end", "https://api.example.com/{# x #}{{ vars.p | urlencode }}", collection.ParamFormatURL, ""},
		{"text either side of a comment joins", "https://api.{# x #}example.com/{{ vars.p | urlencode }}", collection.ParamFormatURL, ""},
		// A raw block is text: it can end the host, and it can hide nothing.
		{"a raw block ends the host", "https://api.example.com{% raw %}/{% endraw %}{{ vars.p | urlencode }}", collection.ParamFormatURL, ""},
		{"an empty raw block before the host", "https://{% raw %}{% endraw %}{{ vars.h }}/p", collection.ParamFormatURL, "scheme and host"},
		// Whitespace control trims the text as it renders, and is judged so.
		{"a trim marker after the host's end", "https://api.example.com/ {{- vars.p | urlencode }}", collection.ParamFormatURL, ""},
		{"a trimmed comment joins the host to data", "https://api.example.com {#- / -#} {{ vars.h }}/p", collection.ParamFormatURL, "scheme and host"},
		{"a set before the host", "{% set h = 'x' %}https://api.example.com/{{ vars.p | urlencode }}", collection.ParamFormatURL, "scheme and host"},
		// A comparison, an in, a not and a test give only true or false,
		// which carry no device syntax, so their operands need no quoting;
		// what reaches the text through ~, and/or or a literal does.
		{"a comparison writes only true or false", "echo {{ vars.env == 'prod' }} {{ 'x' in vars.l }} {{ vars.a is defined }}", collection.ParamFormatCommand, ""},
		{"a literal is the author's own text", "echo {{ 'a;b' }} {{ 3 }}", collection.ParamFormatCommand, ""},
		{"a concatenation carries its operands' text", "echo {{ vars.a ~ '.conf' }}", collection.ParamFormatCommand, "must end with | quote"},
		{"a concatenation quoted whole", "echo {{ (vars.a ~ '.conf') | quote }}", collection.ParamFormatCommand, ""},
		{"or hands back an operand", "echo {{ vars.a or 'x' }}", collection.ParamFormatCommand, "must end with | quote"},
		{"a list literal carries its items", "echo {{ [vars.a] | to_json }}", collection.ParamFormatCommand, "must end with | quote"},
		{"a set boolean reads nothing", "{% set prod = vars.env == 'prod' %}echo {{ prod }}", collection.ParamFormatCommand, ""},
		{"a comparison on device output in a project path", "{% if result.r.ok == true %}a{% endif %}.conf", collection.ParamFormatProjectPath, "never result"},
		{"a defined test on an unknown root", "{% if env.HOME is defined %}x{% endif %}", "", `reads "env"`},
		{"the loop counters read nothing", "{% for f in vars.files %}{{ loop.index }} {{ f | quote }}{% endfor %}", collection.ParamFormatCommand, ""},
	}
	for _, tc := range cases {
		tmpl, err := eng.Compile(tc.source)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		problems := strings.Join(TemplateProblems(tmpl, tc.format), "; ")
		switch {
		case tc.refused == "" && problems != "":
			t.Errorf("%s was refused: %s", tc.name, problems)
		case tc.refused != "" && !strings.Contains(problems, tc.refused):
			t.Errorf("%s: problems %q, want one saying %q", tc.name, problems, tc.refused)
		}
	}
}

// TestTemplateReads_SeesRegistersInsideStatements: a register read only in
// a loop's list or a condition is still a register the task reads, which
// is what the segmenter orders tasks by.
func TestTemplateReads_SeesRegistersInsideStatements(t *testing.T) {
	tmpl, err := render.New().Compile("{% if result.check.ok %}{% for r in nodes.routes %}{{ r }}{% endfor %}{% endif %}")
	if err != nil {
		t.Fatal(err)
	}
	names, all := TemplateReads(tmpl)
	if strings.Join(names, ",") != "check,routes" || all {
		t.Errorf("TemplateReads = %v, all %v; want check,routes and not all", names, all)
	}
}
