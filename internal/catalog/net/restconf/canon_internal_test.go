// Package restconf: tests of the canonical form a change is judged by, and of
// the content checks, apart from a server.
package restconf

import (
	"strings"
	"testing"
)

func TestCanonical_SetsAsideOrderAndWhiteSpaceAndKeepsTheRest(t *testing.T) {
	same := [][2]string{
		{`{"b": 1, "a": [1, 2]}`, `{"a":[1,2],"b":1}`},
		{`{"n": 1.50}`, `{"n":1.50}`},
	}
	for _, pair := range same {
		a, errA := canonical([]byte(pair[0]), true)
		b, errB := canonical([]byte(pair[1]), true)
		if errA != nil || errB != nil || a != b {
			t.Errorf("%s and %s read as different (%v, %v)", pair[0], pair[1], errA, errB)
		}
	}
	differs := [][2]string{
		{`{"a": [1, 2]}`, `{"a": [2, 1]}`},
		{`{"n": 1.5}`, `{"n": 1.50}`},
	}
	for _, pair := range differs {
		a, _ := canonical([]byte(pair[0]), true)
		b, _ := canonical([]byte(pair[1]), true)
		if a == b {
			t.Errorf("%s and %s read as the same", pair[0], pair[1])
		}
	}
	x1 := `<a xmlns="urn:x" xmlns:p="urn:p" y="2" x="1"><!-- note --><b> text </b>
	  <c/></a>`
	x2 := `<q:a xmlns:q="urn:x" x="1" y="2"><q:b>text</q:b><q:c></q:c></q:a>`
	a, errA := canonical([]byte(x1), false)
	b, errB := canonical([]byte(x2), false)
	if errA != nil || errB != nil || a != b {
		t.Errorf("XML differing in prefixes, attribute order, comments and white space read as different:\n%s\n%s", a, b)
	}
	if c, _ := canonical([]byte(`<a xmlns="urn:other"><b>text</b><c/></a>`), false); c == a {
		t.Error("another namespace read as the same")
	}
	for _, bad := range []string{`{`, `{} {}`} {
		if _, err := canonical([]byte(bad), true); err == nil {
			t.Errorf("canonical(%q) accepted", bad)
		}
	}
	if _, err := canonical([]byte(`<a>`), false); err == nil {
		t.Error("XML cut short was accepted")
	}
	if _, err := canonical([]byte(strings.Repeat("<a>", maxCanonDepth+1)), false); err == nil {
		t.Error("XML past the depth bound was accepted")
	}
}

func TestDiffer_FallsBackToBytesAndSaysSo(t *testing.T) {
	var notes []string
	if differ([]byte("{"), []byte(" { "), true, &notes) || len(notes) != 1 {
		t.Errorf("unreadable answers with the same bytes read as a change, or unsaid: %v", notes)
	}
	if !differ([]byte("{"), []byte("["), true, &notes) {
		t.Error("unreadable answers with different bytes read as no change")
	}
}

func TestCheckDocument(t *testing.T) {
	for doc, ok := range map[string]bool{
		`<a/>`:                      true,
		`<?xml version="1.0"?><a/>`: true,
		`text only`:                 false,
		`<!DOCTYPE a><a/>`:          false,
		strings.Repeat("<a>", 300):  false,
		`<a></b>`:                   false,
	} {
		if err := checkDocument(doc, false); (err == nil) != ok {
			t.Errorf("checkDocument(%.20q) = %v, want ok=%v", doc, err, ok)
		}
	}
}

func TestEscapeFor(t *testing.T) {
	if got := escapeFor(`a"b<c>&`, true); got != `a\"b\u003cc\u003e\u0026` {
		t.Errorf("JSON escape = %s", got)
	}
	if got := escapeFor(`a"b<c>&`, false); got != `a&#34;b&lt;c&gt;&amp;` {
		t.Errorf("XML escape = %s", got)
	}
}
