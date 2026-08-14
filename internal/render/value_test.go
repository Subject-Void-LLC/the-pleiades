package render_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestValueShapes covers every Go value shape a caller can put in the
// variable map.
//
// The list is longer than it looks like it needs to be because the values
// arrive from three different places with three different conventions: a
// decoded JSON document (float64 for every number, map[string]any for every
// object), a credential's own flat input map (map[string]string), and Go
// code constructing a map directly (whatever integer type the author
// happened to write). All three have to render the same way, or a value
// changes meaning depending on which layer supplied it.
func TestValueShapes(t *testing.T) {
	t.Parallel()

	type customMap map[string]int

	tests := []struct {
		name   string
		source string
		vars   map[string]any
		want   string
	}{
		{
			name:   "int8",
			source: "{{ v }}",
			vars:   map[string]any{"v": int8(-8)},
			want:   "-8",
		},
		{
			name:   "int16",
			source: "{{ v }}",
			vars:   map[string]any{"v": int16(-16)},
			want:   "-16",
		},
		{
			name:   "int32",
			source: "{{ v }}",
			vars:   map[string]any{"v": int32(-32)},
			want:   "-32",
		},
		{
			name:   "int64",
			source: "{{ v }}",
			vars:   map[string]any{"v": int64(-64)},
			want:   "-64",
		},
		{
			name:   "uint",
			source: "{{ v }}",
			vars:   map[string]any{"v": uint(1)},
			want:   "1",
		},
		{
			name:   "uint8",
			source: "{{ v }}",
			vars:   map[string]any{"v": uint8(8)},
			want:   "8",
		},
		{
			name:   "uint16",
			source: "{{ v }}",
			vars:   map[string]any{"v": uint16(16)},
			want:   "16",
		},
		{
			name:   "uint32",
			source: "{{ v }}",
			vars:   map[string]any{"v": uint32(32)},
			want:   "32",
		},
		{
			name:   "uint64",
			source: "{{ v }}",
			vars:   map[string]any{"v": uint64(64)},
			want:   "64",
		},
		{
			name:   "float32 keeps its shortest round-tripping form",
			source: "{{ v }}",
			vars:   map[string]any{"v": float32(1.5)},
			want:   "1.5",
		},
		{
			name:   "float64 with a fractional part",
			source: "{{ v }}",
			vars:   map[string]any{"v": 0.25},
			want:   "0.25",
		},
		{
			name:   "false renders lowercase",
			source: "{{ v }}",
			vars:   map[string]any{"v": false},
			want:   "false",
		},
		{
			name: "a map type the fast paths do not name still descends",
			// map[string]int is not one of the two shapes descend handles
			// directly, so this exercises the reflect fallback. Without it
			// a caller building a map of counts would get "undefined" for
			// a key that is plainly present.
			source: "{{ counts.retries }}",
			vars:   map[string]any{"counts": map[string]int{"retries": 3}},
			want:   "3",
		},
		{
			name:   "a named map type descends",
			source: "{{ m.k }}",
			vars:   map[string]any{"m": customMap{"k": 7}},
			want:   "7",
		},
		{
			name:   "a slice type the fast paths do not name still indexes",
			source: "{{ ports[1] }}",
			vars:   map[string]any{"ports": []int{22, 443}},
			want:   "443",
		},
		{
			name:   "an array indexes",
			source: "{{ ports[0] }}",
			vars:   map[string]any{"ports": [2]int{22, 443}},
			want:   "22",
		},
		{
			name:   "a string slice indexes through the fast path",
			source: "{{ names[0] }}",
			vars:   map[string]any{"names": []string{"a", "b"}},
			want:   "a",
		},
		{
			name:   "a double-quoted subscript key works like a single-quoted one",
			source: `{{ m["k"] }}`,
			vars:   map[string]any{"m": map[string]any{"k": "v"}},
			want:   "v",
		},
		{
			name:   "an escaped double quote inside a double-quoted literal",
			source: `{{ missing | default("say \"hi\"") }}`,
			vars:   map[string]any{},
			want:   `say "hi"`,
		},
		{
			name: "an integer filter argument parses",
			// The grammar accepts integer literals as filter arguments, so
			// a default of 0 does not have to be written as a string and
			// then coerced by whoever reads it.
			source: "{{ missing | default(0) }}",
			vars:   map[string]any{},
			want:   "0",
		},
		{
			name:   "a negative integer filter argument parses",
			source: "{{ missing | default(-1) }}",
			vars:   map[string]any{},
			want:   "-1",
		},
		{
			name:   "an explicitly empty filter argument list is accepted where arity allows",
			source: "{{ v | upper() }}",
			vars:   map[string]any{"v": "x"},
			want:   "X",
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
			if err != nil {
				t.Fatalf("Render() failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUnsupportedValueShapes covers the shapes that have no text form.
//
// Every one of these renders to something under Go's default formatting,
// and every one of those somethings is the wrong value to inject. A struct
// formatted with %v looks like data and parses as nothing.
func TestUnsupportedValueShapes(t *testing.T) {
	t.Parallel()

	type point struct{ X, Y int }

	tests := []struct {
		name   string
		source string
		vars   map[string]any
	}{
		{
			name:   "a struct has no text form",
			source: "{{ v }}",
			vars:   map[string]any{"v": point{1, 2}},
		},
		{
			name:   "a pointer has no text form",
			source: "{{ v }}",
			vars:   map[string]any{"v": &point{1, 2}},
		},
		{
			name:   "a byte slice has no text form, because it is a list of numbers",
			source: "{{ v }}",
			vars:   map[string]any{"v": []byte("secret")},
		},
		{
			name:   "descending into a struct is undefined rather than reflective field access",
			source: "{{ v.X }}",
			vars:   map[string]any{"v": point{1, 2}},
		},
		{
			name:   "a map with a non-string key cannot be descended by name",
			source: "{{ v.k }}",
			vars:   map[string]any{"v": map[int]string{1: "a"}},
		},
		{
			name:   "indexing a map is undefined",
			source: "{{ v[0] }}",
			vars:   map[string]any{"v": map[string]any{"0": "a"}},
		},
		{
			name:   "naming a key on a slice is undefined",
			source: "{{ v.k }}",
			vars:   map[string]any{"v": []any{"a"}},
		},
		{
			name:   "descending into an explicit null is undefined",
			source: "{{ v.k }}",
			vars:   map[string]any{"v": nil},
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
				t.Fatalf("Render() = %q with no error, want a refusal", got)
			}
			if got != "" {
				t.Errorf("Render() returned %q alongside an error", got)
			}
		})
	}
}

// TestFiltersRefuseValuesWithNoTextForm proves the refusal is applied by
// every filter that needs a string, not only by the one that happened to
// get a test. A filter reaching Go's default map formatting would inject a
// value that looks like data and parses as nothing.
func TestFiltersRefuseValuesWithNoTextForm(t *testing.T) {
	t.Parallel()

	// Every filter in the closed set that coerces its input to text.
	filters := []string{"quote", "b64encode", "b64decode", "lower", "upper", "trim"}

	eng := render.New()
	structured := map[string]any{"v": map[string]any{"k": "value"}}

	for _, name := range filters {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := "{{ v | " + name + " }}"
			tmpl, err := eng.Compile(source)
			if err != nil {
				t.Fatalf("Compile(%q) failed: %v", source, err)
			}

			got, err := tmpl.Render(structured)
			if err == nil {
				t.Fatalf("Render() = %q with no error, want ErrNotRenderable", got)
			}
			if !errors.Is(err, render.ErrNotRenderable) {
				t.Errorf("Render() error = %v, want one matching ErrNotRenderable", err)
			}
		})
	}
}

// TestToJSONRefusesAValueItCannotEncode covers the encoder's own failure
// path. A channel is the cheapest value encoding/json refuses outright.
func TestToJSONRefusesAValueItCannotEncode(t *testing.T) {
	t.Parallel()

	tmpl, err := render.New().Compile("{{ v | to_json }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}

	_, err = tmpl.Render(map[string]any{"v": make(chan int)})
	if err == nil {
		t.Fatal("Render() succeeded on a value JSON cannot encode")
	}
	if !errors.Is(err, render.ErrNotRenderable) {
		t.Errorf("Render() error = %v, want one matching ErrNotRenderable", err)
	}
}

// TestB64DecodeErrorDoesNotQuoteTheOffendingBytes is a disclosure check.
// The standard library's own base64 error quotes the byte that failed, and
// the bytes reaching this filter are a secret. Wrapping that error verbatim
// would put a fragment of it in a log line.
func TestB64DecodeErrorDoesNotQuoteTheOffendingBytes(t *testing.T) {
	t.Parallel()

	const secret = "not-base64-CANARY"

	tmpl, err := render.New().Compile("{{ v | b64decode }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}

	_, err = tmpl.Render(map[string]any{"v": secret})
	if err == nil {
		t.Fatal("Render() succeeded on input that is not base64")
	}
	if strings.Contains(err.Error(), "CANARY") {
		t.Errorf("the error text quotes the offending input: %q", err.Error())
	}
}

// TestIntegerSubscriptOutOfRangeForInt covers the scanner's own overflow
// path, which the digit loop cannot reject on its own.
func TestIntegerSubscriptOutOfRangeForInt(t *testing.T) {
	t.Parallel()

	_, err := render.New().Compile("{{ a[99999999999999999999] }}")
	if err == nil {
		t.Fatal("Compile() accepted an index outside the int range")
	}
	if !errors.Is(err, render.ErrSyntax) {
		t.Errorf("Compile() error = %v, want one matching ErrSyntax", err)
	}
}
