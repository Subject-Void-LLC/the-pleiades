// Tests for the two filters Phase 117a added for rendered task parameters:
// urlencode and cli_token.
package render_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// renderOne compiles and renders source against vars.
func renderOne(t *testing.T, source string, vars map[string]any) (string, error) {
	t.Helper()
	tmpl, err := render.New().Compile(source)
	if err != nil {
		t.Fatalf("Compile(%q): %v", source, err)
	}
	return tmpl.Render(vars)
}

// TestURLEncode: everything outside RFC 3986's unreserved set is encoded,
// "/" included, so a value from a ticket stays one path segment or one
// query value; a value that is not text is refused.
func TestURLEncode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"INC0010001", "INC0010001"},
		{"a b&c=d", "a%20b%26c%3Dd"},
		{"../admin", "..%2Fadmin"},
		{"line\nnext", "line%0Anext"},
		{"already%20encoded", "already%2520encoded"},
		{"café", "caf%C3%A9"},
		{"-._~", "-._~"},
	} {
		got, err := renderOne(t, "{{ v | urlencode }}", map[string]any{"v": tc.in})
		if err != nil || got != tc.want {
			t.Errorf("urlencode(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := renderOne(t, "{{ v | urlencode }}", map[string]any{"v": map[string]any{"a": "b"}}); !errors.Is(err, render.ErrNotRenderable) {
		t.Errorf("urlencode of a mapping: error = %v, want ErrNotRenderable", err)
	}
}

// TestCLIToken: an interface name, an address or a VLAN list passes
// unchanged; a space, a pipe, a question mark, a newline, an empty value or
// an overlong one is refused, and the refusal names no part of the value.
func TestCLIToken(t *testing.T) {
	for _, ok := range []string{"GigabitEthernet1/0/1", "10.0.0.1", "core-sw1.example.net", "10,20,30", "admin@site", "2001:db8::1"} {
		got, err := renderOne(t, "{{ v | cli_token }}", map[string]any{"v": ok})
		if err != nil || got != ok {
			t.Errorf("cli_token(%q) = %q, %v; want it unchanged", ok, got, err)
		}
	}
	for _, bad := range []string{"Gi1/0/1 shutdown", "x | include", "?", "x\nreload", "", strings.Repeat("a", 256), "a;b", "`id`"} {
		_, err := renderOne(t, "{{ v | cli_token }}", map[string]any{"v": bad})
		if !errors.Is(err, render.ErrNotRenderable) {
			t.Errorf("cli_token(%q): error = %v, want ErrNotRenderable", bad, err)
			continue
		}
		if bad != "" && len(bad) < 200 && strings.Contains(err.Error(), bad) {
			t.Errorf("cli_token's refusal echoes the value: %v", err)
		}
	}
}

// TestValue: a template that is one expression and nothing else keeps its
// value's type; any other template reports false and evaluates nothing;
// an undefined name is still an error.
func TestValue(t *testing.T) {
	eng := render.New()
	vars := map[string]any{"nodes": map[string]any{"collect": map[string]any{"sw1": map[string]any{"up": 3.0}}}, "list": []any{"a", "b"}}
	for _, tc := range []struct {
		source string
		want   any
		single bool
	}{
		{"{{ list }}", []any{"a", "b"}, true},
		{"{{ nodes.collect.sw1.up }}", 3.0, true},
		{"  {{ list }}", nil, false},
		{"n={{ nodes.collect.sw1.up }}", nil, false},
		{"plain text", nil, false},
		{"{{ list | to_json }}", `["a","b"]`, true},
	} {
		tmpl, err := eng.Compile(tc.source)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.source, err)
		}
		got, single, err := tmpl.Value(vars)
		if err != nil || single != tc.single {
			t.Fatalf("Value(%q) = %v, %v, %v; want single %v", tc.source, got, single, err, tc.single)
		}
		if single && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Value(%q) = %#v, want %#v", tc.source, got, tc.want)
		}
	}
	tmpl, _ := eng.Compile("{{ missing.key }}")
	if _, single, err := tmpl.Value(vars); !single || !errors.Is(err, render.ErrUndefined) {
		t.Errorf("Value of an undefined name: single %v, error %v; want ErrUndefined", single, err)
	}
}

// TestExpressions: each expression's path, keys and indexes as written,
// and its filters in order, with the text between expressions left out.
func TestExpressions(t *testing.T) {
	tmpl, err := render.New().Compile("https://api/{{ nodes.ticket.json.result[0].sys_id | urlencode }}/x/{{ vars.n }}")
	if err != nil {
		t.Fatal(err)
	}
	got := tmpl.Expressions()
	want := []render.ExpressionInfo{
		{Path: []string{"nodes", "ticket", "json", "result", "0", "sys_id"}, Filters: []string{"urlencode"}},
		{Path: []string{"vars", "n"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expressions() = %#v, want %#v", got, want)
	}
}
