// Package netcfg: EditXML, editing a document's text by its decoded value.
package netcfg

import (
	"strings"
	"testing"
)

// TestEditXML: text is edited decoded, whatever its escaping or a CDATA
// section, and written back escaped; a comment or instruction the edit would
// change is withheld; an attribute or directive it would change is refused;
// what it leaves alone is kept byte for byte.
func TestEditXML(t *testing.T) {
	edit := func(s string) string { return strings.ReplaceAll(s, "k<1", "X") }
	doc := `<?xml version="1.0"?><a b="kept"><c>k&lt;1 and k&#60;1</c><d><![CDATA[k<1]]></d><!-- k<1 --><?pi k<1?><e>other</e></a>`
	got, err := EditXML(doc, edit)
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0"?><a b="kept"><c>X and X</c><d>X</d>` + withheldComment + withheldComment + `<e>other</e></a>`
	if got != want {
		t.Errorf("EditXML = %q\nwant      %q", got, want)
	}
	keep := func(s string) string { return s }
	if got, err := EditXML(doc, keep); err != nil || got != doc {
		t.Errorf("an edit that changes nothing = %q, %v", got, err)
	}
	for name, bad := range map[string]string{
		"an attribute":     `<a b="k&lt;1"/>`,
		"a directive":      `<!DOCTYPE k<1><a/>`,
		"not well-formed":  `<a>`,
		"past the maximum": "<a>" + strings.Repeat("x", MaxXMLBytes) + "</a>",
	} {
		if got, err := EditXML(bad, edit); err == nil || got != "" {
			t.Errorf("%s: EditXML = %q, %v; want an error and nothing", name, got, err)
		}
	}
	if !WellFormedXML("<a/>") || WellFormedXML("<a>") {
		t.Error("WellFormedXML misread a document")
	}
}
