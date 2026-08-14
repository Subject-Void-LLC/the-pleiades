package render_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestRender covers the whole grammar through the public port, which is
// the only surface any caller has. Every case names what it is protecting
// rather than only what it does, because several of them look like
// arbitrary choices until the reason is written down.
func TestRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		vars   map[string]any
		want   string
	}{
		{
			name:   "literal text passes through untouched",
			source: "no actions here",
			vars:   nil,
			want:   "no actions here",
		},
		{
			name:   "a bare reference substitutes its value",
			source: "{{ api_token }}",
			vars:   map[string]any{"api_token": "s3cret"},
			want:   "s3cret",
		},
		{
			name:   "text and actions interleave",
			source: "Bearer {{ token }} for {{ user }}",
			vars:   map[string]any{"token": "abc", "user": "root"},
			want:   "Bearer abc for root",
		},
		{
			name:   "a dotted path descends a nested map",
			source: "{{ tower.filename }}",
			vars:   map[string]any{"tower": map[string]any{"filename": "/run/x"}},
			want:   "/run/x",
		},
		{
			name:   "a dotted path reaches the multi-file spelling",
			source: "{{ tower.filename.cert }}",
			vars: map[string]any{"tower": map[string]any{
				"filename": map[string]any{"cert": "/run/x.cert"},
			}},
			want: "/run/x.cert",
		},
		{
			name:   "a bracketed string key means the same as a dot",
			source: "{{ tower['filename'] }}",
			vars:   map[string]any{"tower": map[string]any{"filename": "/run/x"}},
			want:   "/run/x",
		},
		{
			name:   "a bracketed index reads a list element",
			source: "{{ hosts[1] }}",
			vars:   map[string]any{"hosts": []any{"a", "b", "c"}},
			want:   "b",
		},
		{
			name:   "a flat string map descends without reflection",
			source: "{{ inputs.password }}",
			vars:   map[string]any{"inputs": map[string]string{"password": "pw"}},
			want:   "pw",
		},
		{
			name:   "whitespace control trims around the action",
			source: "a  {{- x -}}  b",
			vars:   map[string]any{"x": "X"},
			want:   "aXb",
		},
		{
			name:   "default rescues an undefined reference",
			source: "{{ region | default('us-east-1') }}",
			vars:   map[string]any{},
			want:   "us-east-1",
		},
		{
			name:   "default is a no-op when the reference is defined",
			source: "{{ region | default('us-east-1') }}",
			vars:   map[string]any{"region": "eu-west-2"},
			want:   "eu-west-2",
		},
		{
			name:   "default rescues a path whose intermediate key is missing",
			source: "{{ a.b.c | default('fallback') }}",
			vars:   map[string]any{"a": map[string]any{}},
			want:   "fallback",
		},
		{
			name:   "default rescues an out-of-range index",
			source: "{{ hosts[9] | default('none') }}",
			vars:   map[string]any{"hosts": []any{"a"}},
			want:   "none",
		},
		{
			name:   "quote wraps a plain value in single quotes",
			source: "{{ pw | quote }}",
			vars:   map[string]any{"pw": "hunter2"},
			want:   "'hunter2'",
		},
		{
			name: "quote handles an embedded apostrophe, which single quotes cannot escape",
			// A password containing an apostrophe is common, and getting
			// this wrong produces a shell file that silently truncates the
			// value at the apostrophe.
			source: "{{ pw | quote }}",
			vars:   map[string]any{"pw": "it's"},
			want:   `'it'\''s'`,
		},
		{
			name:   "to_json encodes a nested structure",
			source: "{{ cfg | to_json }}",
			vars:   map[string]any{"cfg": map[string]any{"k": "v"}},
			want:   `{"k":"v"}`,
		},
		{
			name:   "tojson is accepted as an alias",
			source: "{{ cfg | tojson }}",
			vars:   map[string]any{"cfg": []any{1, 2}},
			want:   `[1,2]`,
		},
		{
			name: "to_json does not HTML-escape",
			// The default encoder escapes <, > and & for browsers. This
			// output goes into an environment variable, where the escaped
			// form is simply the wrong value.
			source: "{{ s | to_json }}",
			vars:   map[string]any{"s": "a<b&c"},
			want:   `"a<b&c"`,
		},
		{
			name:   "b64encode encodes",
			source: "{{ k | b64encode }}",
			vars:   map[string]any{"k": "hello"},
			want:   "aGVsbG8=",
		},
		{
			name:   "b64decode decodes",
			source: "{{ k | b64decode }}",
			vars:   map[string]any{"k": "aGVsbG8="},
			want:   "hello",
		},
		{
			name:   "lower, upper and trim normalize text",
			source: "{{ a | lower }}/{{ b | upper }}/{{ c | trim }}",
			vars:   map[string]any{"a": "EU-WEST", "b": "eu-west", "c": "  padded\n"},
			want:   "eu-west/EU-WEST/padded",
		},
		{
			name:   "filters chain left to right",
			source: "{{ k | trim | upper | b64encode }}",
			vars:   map[string]any{"k": "  ab  "},
			want:   "QUI=",
		},
		{
			name:   "a redundant default past the first position is accepted",
			source: "{{ k | upper | default('x') }}",
			vars:   map[string]any{"k": "v"},
			want:   "V",
		},
		{
			name:   "a boolean renders lowercase, as JSON and Ansible expect",
			source: "{{ verify }}",
			vars:   map[string]any{"verify": true},
			want:   "true",
		},
		{
			name:   "an integer renders in decimal",
			source: "{{ port }}",
			vars:   map[string]any{"port": 8443},
			want:   "8443",
		},
		{
			name: "a whole float renders without an exponent",
			// A port number decoded from JSON arrives as a float64, and
			// "8443" is the only form anything downstream accepts.
			source: "{{ port }}",
			vars:   map[string]any{"port": float64(8443)},
			want:   "8443",
		},
		{
			name:   "escape sequences in a filter argument are decoded",
			source: `{{ missing | default('a\nb\t\\c\'d') }}`,
			vars:   map[string]any{},
			want:   "a\nb\t\\c'd",
		},
		{
			name:   "a pipe inside a string literal is not a filter separator",
			source: `{{ missing | default('a|b') }}`,
			vars:   map[string]any{},
			want:   "a|b",
		},
		{
			name:   "an empty template renders empty",
			source: "",
			vars:   nil,
			want:   "",
		},
		{
			name: "the parity corpus custom type's own injector templates",
			// tests/parity/testdata/credential_types/custom-rest-api-token.json.
			// This is the shape that has to work for an AWX migration.
			source: "{{ api_token }}",
			vars:   map[string]any{"api_token": "T", "api_url": "U"},
			want:   "T",
		},
	}

	eng := render.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := eng.Compile(tt.source)
			if err != nil {
				t.Fatalf("Compile(%q) returned an unexpected error: %v", tt.source, err)
			}

			got, err := tmpl.Render(tt.vars)
			if err != nil {
				t.Fatalf("Render() returned an unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCompileRefusals covers everything the grammar rejects at compile
// time. Each case asserts the sentinel a caller checks with errors.Is,
// because the API contract is the sentinel and not the message text.
func TestCompileRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr error
	}{
		{
			name: "a statement block is refused rather than passed through",
			// Passing it through as literal text would render a loop's
			// source code where its output belonged, with nothing to
			// suggest the renderer had not run it.
			source:  "{% for x in y %}{{ x }}{% endfor %}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "a comment block is refused",
			source:  "{# nothing to see #}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "an unterminated action is refused",
			source:  "{{ x",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "a nested opening delimiter is refused",
			source:  "{{ a {{ b }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "an empty action has no expression",
			source:  "{{}}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "an action holding only whitespace has no expression",
			source:  "{{   }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "a reference may not start with a digit",
			source:  "{{ 1abc }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "trailing junk after a path is refused",
			source:  "{{ a b }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "an unterminated subscript is refused",
			source:  "{{ a[0 }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "an unterminated string literal is refused",
			source:  "{{ a | default('x }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "an unsupported escape sequence is refused",
			source:  `{{ a | default('\q') }}`,
			wantErr: render.ErrSyntax,
		},
		{
			name:    "an unterminated filter argument list is refused",
			source:  "{{ a | default('x' }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "a dangling pipe is refused",
			source:  "{{ a | }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "a filter outside the closed set is refused",
			source:  "{{ a | regex_replace('x','y') }}",
			wantErr: render.ErrUnknownFilter,
		},
		{
			name:    "a filesystem-reading filter is refused",
			source:  "{{ a | lookup('file') }}",
			wantErr: render.ErrUnknownFilter,
		},
		{
			name:    "default without its argument is refused",
			source:  "{{ a | default }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "default with too many arguments is refused",
			source:  "{{ a | default('x','y') }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "a zero-argument filter given an argument is refused",
			source:  "{{ a | upper('x') }}",
			wantErr: render.ErrSyntax,
		},
		{
			name:    "source past the size limit is refused",
			source:  strings.Repeat("x", 64<<10+1),
			wantErr: render.ErrTooLarge,
		},
		{
			name:    "a path deeper than the limit is refused",
			source:  "{{ a" + strings.Repeat(".b", 17) + " }}",
			wantErr: render.ErrTooLarge,
		},
	}

	eng := render.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := eng.Compile(tt.source)
			if err == nil {
				t.Fatalf("Compile(%q) succeeded, want %v", tt.source, tt.wantErr)
			}
			if tmpl != nil {
				t.Errorf("Compile() returned a non-nil Template alongside an error")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Compile() error = %v, want one matching %v", err, tt.wantErr)
			}
		})
	}
}

// TestPathDepthLimitIsExpressedAsSyntax pins the boundary rather than only
// the far side of it, so a future change to maxPathDepth cannot silently
// move where the refusal starts.
func TestPathDepthAtTheLimitCompiles(t *testing.T) {
	t.Parallel()

	source := "{{ a" + strings.Repeat(".b", 16) + " }}"
	if _, err := render.New().Compile(source); err != nil {
		t.Fatalf("Compile() at exactly the depth limit failed: %v", err)
	}
}
