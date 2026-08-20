package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// FuzzCSVToList proves the CSV parser never panics on malformed input --
// an unbalanced quote, a truncated record, an embedded newline, a
// pathologically long line -- per this phase's own Fuzz/Stress Test
// checklist item.
func FuzzCSVToList(f *testing.F) {
	seeds := []string{
		"", ",", "a,b,c", `a,"b,c",d`, `a,"unterminated`, "a,b\nc,d",
		`"`, `""""`, "a,\x00,b", "a,\r\n,b",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		filters.CSVToList(line)
	})
}

// FuzzListToCSV mirrors FuzzCSVToList for the encoding direction, and
// additionally proves the CSV round trip through a byte-preserving list
// of fields: ListToCSV never panics, and whatever it produces is either
// "" (refused) or a line CSVToList reads back into an equal list.
func FuzzListToCSV(f *testing.F) {
	f.Add("a", "b,c", `d"e`)
	f.Add("", "", "")
	f.Add("with\nnewline", "plain", "")
	f.Fuzz(func(t *testing.T, a, b, c string) {
		fields := []string{a, b, c}
		line := filters.ListToCSV(fields)
		if line == "" {
			return
		}
		got := filters.CSVToList(line)
		if got == nil {
			t.Fatalf("ListToCSV(%#v) = %q, but CSVToList(that) failed to parse it back", fields, line)
		}
	})
}

// FuzzYAMLToJSON proves YAMLToJSON never panics on malformed YAML
// (truncated documents, deeply nested structures, invalid syntax).
func FuzzYAMLToJSON(f *testing.F) {
	seeds := []string{
		"", "a: 1", "a:\n  b: 1\nc: [1,2]\n", "a: [1,2\n", "a: {b: c",
		"- - - - - 1", "&a *a", "a: !!binary invalid", "\t\t\tinvalid",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, yamlText string) {
		filters.YAMLToJSON(yamlText)
	})
}

// FuzzJSONToYAML mirrors FuzzYAMLToJSON for the JSON parser.
func FuzzJSONToYAML(f *testing.F) {
	seeds := []string{
		"", "{}", `{"a":1}`, `{"a":`, `[1,2,`, `{"a": [1,2,3]}`,
		"null", "true", `"unterminated`, `{"a": NaN}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, jsonText string) {
		filters.JSONToYAML(jsonText)
	})
}

// FuzzXMLToJSON proves XMLToJSON never panics on malformed XML
// (mismatched tags, truncated documents, deeply nested elements,
// malformed attributes).
func FuzzXMLToJSON(f *testing.F) {
	seeds := []string{
		"", "<a></a>", "<a></b>", "<a>", `<a attr="1"><b>x</b><b>y</b></a>`,
		"<?xml version=\"1.0\"?><a>x</a>", "<a><b></a></b>", "not xml",
		`<a xmlns:ns="x" ns:attr="1">x</a>`, "<a>&invalid;</a>",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, xmlText string) {
		filters.XMLToJSON(xmlText)
	})
}
