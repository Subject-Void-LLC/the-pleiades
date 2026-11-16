// Package restconf: tests of how a change is judged from two reads and of how
// a secret's value is escaped into content, apart from a server. The canonical
// form itself is pkg/yangdoc's, tested there.
package restconf

import (
	"testing"
)

func TestDiffer_FallsBackToBytesAndSaysSo(t *testing.T) {
	var notes []string
	if differ([]byte("{"), []byte(" { "), true, &notes) || len(notes) != 1 {
		t.Errorf("unreadable answers with the same bytes read as a change, or unsaid: %v", notes)
	}
	if !differ([]byte("{"), []byte("["), true, &notes) {
		t.Error("unreadable answers with different bytes read as no change")
	}
	if differ([]byte(`{"a": 1}`), []byte(`{"a":1}`), true, &notes) {
		t.Error("the same JSON spelled two ways read as a change")
	}
	if !differ([]byte(`<a xmlns="urn:x"><b>1</b></a>`), []byte(`<a xmlns="urn:x"><b>2</b></a>`), false, &notes) {
		t.Error("XML with another value read as no change")
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
