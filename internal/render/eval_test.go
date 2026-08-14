package render_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestRenderRefusals covers every way rendering fails after a template has
// compiled cleanly. The strict-undefined cases are the reason this package
// exists, so they come first and they assert the returned output as well as
// the error.
func TestRenderRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		vars    map[string]any
		wantErr error
	}{
		{
			name:    "an unsupplied name is an error, never the empty string",
			source:  "{{ api_token }}",
			vars:    map[string]any{},
			wantErr: render.ErrUndefined,
		},
		{
			name:    "a nil variable map is an error, not an empty render",
			source:  "{{ api_token }}",
			vars:    nil,
			wantErr: render.ErrUndefined,
		},
		{
			name:    "a missing intermediate key is undefined",
			source:  "{{ a.b.c }}",
			vars:    map[string]any{"a": map[string]any{}},
			wantErr: render.ErrUndefined,
		},
		{
			name:    "descending into a scalar is undefined",
			source:  "{{ a.b }}",
			vars:    map[string]any{"a": "not a map"},
			wantErr: render.ErrUndefined,
		},
		{
			name:    "an out-of-range index is undefined",
			source:  "{{ a[5] }}",
			vars:    map[string]any{"a": []any{"only one"}},
			wantErr: render.ErrUndefined,
		},
		{
			name:    "a negative index is undefined rather than reading from the end",
			source:  "{{ a[-1] }}",
			vars:    map[string]any{"a": []any{"x", "y"}},
			wantErr: render.ErrUndefined,
		},
		{
			name: "default does not rescue when it is not the first filter",
			// Jinja2 behaves the same way: lower runs first and has
			// nothing to lowercase. Accepting it here would quietly change
			// what an imported AWX template evaluates to.
			source:  "{{ missing | lower | default('x') }}",
			vars:    map[string]any{},
			wantErr: render.ErrUndefined,
		},
		{
			name: "a null value is not the empty string",
			// Supplying null is a different act from supplying nothing, so
			// it gets a different error rather than being folded into the
			// undefined case or silently rendered as "".
			source:  "{{ token }}",
			vars:    map[string]any{"token": nil},
			wantErr: render.ErrNotRenderable,
		},
		{
			name:    "a map has no text form without to_json",
			source:  "{{ cfg }}",
			vars:    map[string]any{"cfg": map[string]any{"k": "v"}},
			wantErr: render.ErrNotRenderable,
		},
		{
			name:    "a list has no text form without to_json",
			source:  "{{ cfg }}",
			vars:    map[string]any{"cfg": []any{1, 2}},
			wantErr: render.ErrNotRenderable,
		},
		{
			name:    "b64decode refuses input that is not base64",
			source:  "{{ k | b64decode }}",
			vars:    map[string]any{"k": "not base64 at all!"},
			wantErr: render.ErrNotRenderable,
		},
		{
			name:    "a filter applied to a structure reports the structure, not a formatted map",
			source:  "{{ cfg | upper }}",
			vars:    map[string]any{"cfg": map[string]any{"k": "v"}},
			wantErr: render.ErrNotRenderable,
		},
		{
			name:    "output past the size limit is refused",
			source:  "{{ big }}{{ big }}",
			vars:    map[string]any{"big": strings.Repeat("x", (1<<20)/2+1)},
			wantErr: render.ErrTooLarge,
		},
	}

	eng := render.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := eng.Compile(tt.source)
			if err != nil {
				t.Fatalf("Compile(%q) failed: %v", tt.source, err)
			}

			got, err := tmpl.Render(tt.vars)
			if err == nil {
				t.Fatalf("Render() = %q with no error, want %v", got, tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Render() error = %v, want one matching %v", err, tt.wantErr)
			}
			// A partially rendered prefix is indistinguishable from a
			// complete value at the call site, and this package's callers
			// inject what they are given.
			if got != "" {
				t.Errorf("Render() returned %q alongside an error, want the empty string", got)
			}
		})
	}
}

// TestUndefinedErrorNamesTheReferenceAndNotAValue is a security assertion,
// not an ergonomics one. This error is logged, returned over HTTP, and
// attached to a job record, so a value inside it would defeat every masking
// control downstream.
func TestUndefinedErrorNamesTheReferenceAndNotAValue(t *testing.T) {
	t.Parallel()

	const secret = "sup3rs3cr3t-value"

	tmpl, err := render.New().Compile("{{ creds.token }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}

	// "creds" is supplied and holds a secret; "token" is the missing key.
	// A naive error that dumped the container it failed to index would
	// leak the sibling value.
	_, err = tmpl.Render(map[string]any{
		"creds": map[string]any{"password": secret},
	})
	if err == nil {
		t.Fatal("Render() succeeded, want an undefined error")
	}

	var undef *render.UndefinedError
	if !errors.As(err, &undef) {
		t.Fatalf("Render() error = %T, want a *render.UndefinedError", err)
	}
	if undef.Name != "creds.token" {
		t.Errorf("UndefinedError.Name = %q, want %q", undef.Name, "creds.token")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error text contains the sibling secret value: %q", err.Error())
	}
}

// TestNames proves Names reports what a caller needs to validate a template
// at save time, which is the whole reason the method exists.
func TestNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name:   "a template with no actions references nothing",
			source: "just text",
			want:   []string{},
		},
		{
			name:   "one reference",
			source: "{{ api_token }}",
			want:   []string{"api_token"},
		},
		{
			name:   "names are sorted and deduplicated",
			source: "{{ b }}{{ a }}{{ b }}",
			want:   []string{"a", "b"},
		},
		{
			name: "only the root of a path is reported",
			// A caller validates against the declared input ids, and
			// "tower" is the name that has to be declared. The rest of the
			// path is structure inside that value.
			source: "{{ tower.filename.cert }}",
			want:   []string{"tower"},
		},
		{
			name: "a name guarded by default is still reported",
			// It is optional, not absent. A validator still has to accept
			// it as a declared input, so hiding it here would make an
			// optional input look like a typo.
			source: "{{ region | default('us-east-1') }}",
			want:   []string{"region"},
		},
		{
			name:   "a filter argument is not a name",
			source: "{{ a | default('b') }}",
			want:   []string{"a"},
		},
	}

	eng := render.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := eng.Compile(tt.source)
			if err != nil {
				t.Fatalf("Compile(%q) failed: %v", tt.source, err)
			}

			got := tmpl.Names()
			if len(got) != len(tt.want) {
				t.Fatalf("Names() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("Names() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestNamesReturnsACopy guards a real hazard: one Template is shared by
// every caller that compiled the same source, so handing out the backing
// array would let one caller's append corrupt another caller's view.
func TestNamesReturnsACopy(t *testing.T) {
	t.Parallel()

	tmpl, err := render.New().Compile("{{ a }}{{ b }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}

	first := tmpl.Names()
	first[0] = "mutated"

	second := tmpl.Names()
	if second[0] != "a" {
		t.Errorf("mutating one Names() result changed the next: got %q, want %q", second[0], "a")
	}
}

// TestSourceRoundTrips proves Source returns the text unchanged, including
// the whitespace an author wrote, so an audit record quotes what was saved.
func TestSourceRoundTrips(t *testing.T) {
	t.Parallel()

	const source = "  {{-  api_token  -}}  "

	tmpl, err := render.New().Compile(source)
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if tmpl.Source() != source {
		t.Errorf("Source() = %q, want %q", tmpl.Source(), source)
	}
}
