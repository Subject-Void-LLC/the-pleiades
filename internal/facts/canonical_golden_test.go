// Tests that facts.Canonical writes the bytes it wrote before encoding/json
// came to run on the v2 implementation (Go 1.27).
package facts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestCanonicalBytesAreThoseGo126Wrote: every fact hash and chain hash is
// SHA-256 over Canonical's bytes, so a change in them breaks every chain a
// store holds. Go 1.27 runs encoding/json on its v2 implementation, which
// keeps v1's behavior; this holds it to that over the values an encoder
// rewrite most often changes (HTML escaping, invalid UTF-8, the line and
// paragraph separators, controls, floats, integers past 2^53, nil against
// empty, nested maps, bytes). testdata/canonical-go1.26.json is what
// go1.26.10 wrote for exactly these values, kept as a file so no tool
// reinterprets an escape in it; go1.27.3 wrote the same bytes (2026-11-08).
func TestCanonicalBytesAreThoseGo126Wrote(t *testing.T) {
	values := map[string]any{
		"html":       `<a href="x">&</a>`,
		"utf8bad":    "ok\xff\xfeend",
		"separators": "a\u2028b\u2029c",
		"controls":   "\x00\x01\x1f\x7f",
		"unicode":    "\u00e9\u4e2d\U0001d11e",
		"floats":     []any{0.1, 1.5e300, 1e21, 1e-7, -0.0, 123456789.0, 3.0},
		"ints":       []any{int64(9007199254740993), int64(-9223372036854775808), uint64(18446744073709551615)},
		"numberstr":  json.Number("12345678901234567890.000"),
		"empty":      []any{},
		"nilslice":   []string(nil),
		"nilmap":     map[string]any(nil),
		"nested":     map[string]any{"z": 1, "a": map[string]any{"y": true, "b": nil}},
		"bytes":      []byte{0, 1, 2, 255},
	}
	want, err := os.ReadFile("testdata/canonical-go1.26.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := facts.Canonical(values)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Canonical writes other bytes than Go 1.26 did, so every stored chain would break:\n got  %q\n want %q", got, want)
	}
}
