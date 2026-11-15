// Package netcfg: EditJSON and KeepsForm, the value-by-value editing a
// caller masking by value (pkg/restconf's credential masking) goes through,
// and the check that a masked copy still reads as the form it was in.
package netcfg

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEditJSON_ReplacesWholeValuesAndKeepsTheRest: a key, a string and a
// number the edit changes are written back whole, as JSON strings, and every
// other byte, the layout included, is where it was.
func TestEditJSON_ReplacesWholeValuesAndKeepsTheRest(t *testing.T) {
	doc := "{\n  \"a-1234\": [1234, 12345, \"x 1234 y\", true, null],\n  \"b\": {\"c\": 1.5}\n}\n"
	var numbers []string
	got, err := EditJSON(doc, func(v string, number bool) string {
		if number {
			numbers = append(numbers, v)
		}
		return strings.ReplaceAll(v, "1234", "N")
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a-N\": [\"N\", \"N5\", \"x N y\", true, null],\n  \"b\": {\"c\": 1.5}\n}\n"
	if got != want || !json.Valid([]byte(got)) {
		t.Errorf("EditJSON = %q, want %q", got, want)
	}
	if strings.Join(numbers, ",") != "1234,12345,1.5" {
		t.Errorf("numbers handed to the edit = %v, want each as written", numbers)
	}
}

// TestEditJSON_RefusesWhatItCannotReadWhole: nothing, a document cut short,
// one with text or a second value after it, and an object holding a key
// twice each return an error and none of the text.
func TestEditJSON_RefusesWhatItCannotReadWhole(t *testing.T) {
	keep := func(v string, _ bool) string { return v }
	for _, doc := range []string{"", "{", `{"a": 1`, `{"a": 1} trailing`, `{"a": 1} {"b": 2}`, `1 2`, `{"a": 1, "a": 2}`} {
		if got, err := EditJSON(doc, keep); err == nil || got != "" {
			t.Errorf("EditJSON(%q) = %q, %v; want an error and nothing", doc, got, err)
		}
	}
	if got, err := EditJSON(`"x"`, keep); err != nil || got != `"x"` {
		t.Errorf("a top-level string = %q, %v", got, err)
	}
}

// TestKeepsForm holds each form to itself: JSON that stops parsing and XML
// that stops reading are refused, and text, or a form that still reads, is
// not.
func TestKeepsForm(t *testing.T) {
	for name, tc := range map[string]struct {
		out, masked string
		refused     bool
	}{
		"JSON still JSON":   {`{"a": 1}`, `{"a": "x"}`, false},
		"JSON broken":       {`{"a": 1}`, `{"a": x}`, true},
		"XML still XML":     {`<a>1</a>`, `<a>x</a>`, false},
		"XML broken":        {`<a>1</a>`, `<a>x</b>`, true},
		"text is not held":  {`a 1`, `a {`, false},
		"broken XML before": {`<a>1`, `<a>x</b>`, false},
	} {
		if err := KeepsForm(tc.out, tc.masked); (err != nil) != tc.refused {
			t.Errorf("%s: KeepsForm = %v, want refused %v", name, err, tc.refused)
		}
	}
}

// TestNumeric: digits, signed once at most, are a number; anything else is
// not.
func TestNumeric(t *testing.T) {
	for s, want := range map[string]bool{"100": true, "-100": true, "+7": true, "": false, "-": false, "--1": false, "1.5": false, "10.255.100.1": false, "1e9": false} {
		if got := numeric(s); got != want {
			t.Errorf("numeric(%q) = %v, want %v", s, got, want)
		}
	}
}
