// Fuzzing the JSON reader and writer with arbitrary documents.
package docedit

import (
	"encoding/json"
	"testing"
)

// FuzzJSONRoundTrip: a document that parses is written back as valid
// JSON that parses to the same document, in every layout, and writing is
// stable.
func FuzzJSONRoundTrip(f *testing.F) {
	for _, seed := range []string{`{}`, `[]`, `{"a":[1,2.5e3,-0,"x\u0000"],"b":{"c":null}}`, "{\n\t\"a\": true\n}\n", `"s"`, `12345678901234567890123`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, format, err := ParseJSON(data)
		if err != nil {
			return
		}
		for _, fm := range []JSONFormat{format, {}, {Indent: "  ", Trailing: true}} {
			out := EncodeJSON(v, fm)
			if !json.Valid(out) {
				t.Fatalf("invalid JSON written: %q", out)
			}
			back, _, err := ParseJSON(out)
			if err != nil {
				t.Fatalf("the written document does not parse: %v", err)
			}
			if !SameJSON(v, back) || string(EncodeJSON(back, fm)) != string(out) {
				t.Fatalf("not stable: %q", out)
			}
		}
	})
}

// FuzzYAMLRoundTrip: a document that parses is written back as a document
// that parses again; nothing panics.
func FuzzYAMLRoundTrip(f *testing.F) {
	for _, seed := range []string{"a: 1\n", "# c\nl:\n  - 1\n  - {b: x}\n", "a: &x 1\nb: *x\n", "", "---\n", "k: |\n  text\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := ParseYAML(data)
		if err != nil {
			return
		}
		out, err := d.Encode()
		if err != nil {
			return
		}
		if _, err := ParseYAML(out); err != nil {
			t.Fatalf("written %q does not parse: %v", out, err)
		}
	})
}
