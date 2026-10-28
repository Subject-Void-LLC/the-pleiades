// Package render_test: trim_blocks and lstrip_blocks against Jinja's
// output, their place in the compile cache key, and their fuzz target.
package render_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestBlockOptions renders each template under trim_blocks and
// lstrip_blocks with the output Jinja2 gives under the same settings.
func TestBlockOptions(t *testing.T) {
	t.Parallel()
	trim := render.Options{TrimBlocks: true}
	lstrip := render.Options{LstripBlocks: true}
	both := render.Options{TrimBlocks: true, LstripBlocks: true}
	vars := map[string]any{"a": true, "hosts": []any{"a", "b"}, "vars": map[string]any{"backends": []any{"app1", "app2"}, "tls": true}}
	tests := []struct {
		name   string
		source string
		opts   render.Options
		want   string
	}{
		{"off by default", "{% if a %}\nyes\n{% endif %}\nend", render.Options{}, "\nyes\n\nend"},
		{"trim removes the newline after a tag", "{% if a %}\nyes\n{% endif %}\nend", trim, "yes\nend"},
		{"trim removes one newline only", "{% if a %}\n\nyes{% endif %}", trim, "\nyes"},
		{"trim takes a CRLF as one newline", "{% if a %}\r\nyes{% endif %}", trim, "yes"},
		{"trim leaves an expression's newline", "{{ a }}\nx", trim, "true\nx"},
		{"trim after a comment", "{# c #}\nx", trim, "x"},
		{"+%} keeps the newline", "{% if a +%}\nx{% endif %}", trim, "\nx"},
		{"a loop of lines", "{% for h in hosts %}\n{{ h }}\n{% endfor %}\nend\n", trim, "a\nb\nend\n"},
		{"lstrip strips an indented tag", "  {% if a %}x{% endif %}", lstrip, "x"},
		{"lstrip only at a line's start", "x {% if a %}y{% endif %}", lstrip, "x y"},
		{"lstrip after a newline", "a\n\t {% if a %}b{% endif %}", lstrip, "a\nb"},
		{"lstrip leaves an expression's indentation", "  {{ a }}", lstrip, "  true"},
		{"{%+ keeps the indentation", "  {%+ if a %}x{% endif %}", lstrip, "  x"},
		{"lstrip strips a comment's indentation", "  {# c #}x", lstrip, "x"},
		{"both, indented blocks", "<ul>\n  {% for h in hosts %}\n  <li>{{ h }}</li>\n  {% endfor %}\n</ul>\n", both, "<ul>\n  <li>a</li>\n  <li>b</li>\n</ul>\n"},
		{"both, a line starting after a trimmed tag", "{% if a %}\n  {% if a %}\nx\n  {% endif %}\n{% endif %}\n", both, "x\n"},
		{"- still trims everything", "a  {%- if a -%}  \n b{% endif %}", both, "ab"},
		{"raw keeps its opening newline", "{% raw %}\n{{ x }}\n{% endraw %}\nz", trim, "\n{{ x }}\nz"},
		{"lstrip before an endraw", "  {% raw %}a\n  {% endraw %}", lstrip, "a\n"},
		{"+ on an endraw", "{% raw %}a{% endraw +%}\nz", trim, "a\nz"},
		{"the docs page's example", docsExample, trim, "upstream app {\n  server app1:8080;\n  server app2:8080;\n}\nlisten 443 ssl;\n"},
	}
	eng := render.New()
	for _, tt := range tests {
		tmpl, err := eng.CompileWith(tt.source, tt.opts)
		if err != nil {
			t.Errorf("%s: Compile(%q): %v", tt.name, tt.source, err)
			continue
		}
		got, err := tmpl.Render(vars)
		if err != nil || got != tt.want {
			t.Errorf("%s: Render(%q) = %q, %v; want %q", tt.name, tt.source, got, err, tt.want)
		}
	}
}

// TestBlockOptionsAreInTheCacheKey holds that one source compiled under two
// settings is two templates, and Compile is CompileWith with both off.
func TestBlockOptionsAreInTheCacheKey(t *testing.T) {
	t.Parallel()
	eng := render.New()
	source := "{% if a %}\nx{% endif %}"
	plain, err := eng.Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	trimmed, err := eng.CompileWith(source, render.Options{TrimBlocks: true})
	if err != nil {
		t.Fatal(err)
	}
	same, err := eng.CompileWith(source, render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if same != plain {
		t.Error("CompileWith with both options off did not share Compile's template")
	}
	vars := map[string]any{"a": true}
	p, _ := plain.Render(vars)
	q, _ := trimmed.Render(vars)
	if p != "\nx" || q != "x" {
		t.Errorf("the two settings rendered %q and %q, want %q and %q", p, q, "\nx", "x")
	}
}

// FuzzCompileWithOptions holds the parse under both block options: it
// never panics, returns exactly one of a template and an error, keeps the
// output bound, and leaves a template with no statement or comment exactly
// as it renders without the options.
func FuzzCompileWithOptions(f *testing.F) {
	for _, s := range []string{
		"{% if a %}\nx\n{% endif %}\n", "  {% for x in a %}\n  {{ x }}\n  {% endfor %}\n",
		"{%+ if a +%}\n{%- endif -%}", "{# c #}\n  {#+ d +#}\n", "{% raw %}\n  {% endraw +%}\n",
		"\r\n  {% if a %}\r\n", "{{ a }}\n  {{ b }}\n", "  {%", "{% set x = a %}\n\n\t{{ x }}",
		strings.Repeat("  {% if a %}\n", 16) + strings.Repeat("  {% endif %}\n", 16),
	} {
		f.Add(s, true, false)
		f.Add(s, true, true)
		f.Add(s, false, true)
	}
	eng := render.New()
	f.Fuzz(func(t *testing.T, source string, trim, lstrip bool) {
		opts := render.Options{TrimBlocks: trim, LstripBlocks: lstrip}
		tmpl, err := eng.CompileWith(source, opts)
		if (tmpl == nil) == (err == nil) {
			t.Fatalf("CompileWith(%q) returned template=%v err=%v", source, tmpl != nil, err)
		}
		if err != nil {
			return
		}
		vars := map[string]any{}
		for _, n := range tmpl.Names() {
			vars[n] = "sentinel-" + n
		}
		out, err := tmpl.Render(vars)
		if err != nil {
			return
		}
		if len(out) > maxOutputBytesUnderTest {
			t.Fatalf("Render() produced %d bytes", len(out))
		}
		if strings.Contains(source, "{%") || strings.Contains(source, "{#") {
			return
		}
		plain, err := eng.Compile(source)
		if err != nil {
			t.Fatalf("Compile(%q) failed where CompileWith succeeded: %v", source, err)
		}
		if again, _ := plain.Render(vars); again != out {
			t.Fatalf("the options changed a template with no statement: %q then %q", again, out)
		}
	})
}
