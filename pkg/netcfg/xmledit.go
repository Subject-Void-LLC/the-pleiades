// Package netcfg: editing an XML document's text by its decoded value, so a
// caller masking by value (pkg/restconf's credential masking) finds a value
// however the device escaped it (&apos;, &quot;, &#39;, &#x27;, a CDATA
// section) and every edit is written back escaped, leaving the document
// well-formed. Text replacement over the escaped bytes finds only the
// escapings it was told about.
package netcfg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// WellFormedXML reports whether doc reads to its end as XML, under the strict
// decoder MaskXML reads with.
func WellFormedXML(doc string) bool {
	return wellFormed(doc) == nil
}

// EditXML is doc, a well-formed XML document, with edit applied to the
// decoded text of each run of character data, every run edit changed written
// back escaped in its place. A comment or processing instruction whose text
// edit would change is withheld whole, as MaskXML withholds every comment. An
// attribute value or a directive edit would change returns an error and none
// of the text, since neither can be replaced in place. The result is checked
// to read back as XML.
func EditXML(doc string, edit func(string) string) (string, error) {
	if len(doc) > MaxXMLBytes {
		return "", fmt.Errorf("the document is %d bytes, more than the %d masking reads", len(doc), MaxXMLBytes)
	}
	d := newDecoder(doc)
	var edits []xmlEdit
	for {
		start := int(d.InputOffset())
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", xmlError(err)
		}
		end := int(d.InputOffset())
		switch t := tok.(type) {
		case xml.CharData:
			if text := string(t); edit(text) != text {
				edits = append(edits, xmlEdit{start: start, end: end, text: escapeXMLText(edit(text))})
			}
		case xml.StartElement:
			for _, a := range t.Attr {
				if edit(a.Value) != a.Value {
					return "", fmt.Errorf("a <%s> element's %q attribute holds a value masking replaces, which it cannot replace in place", t.Name.Local, a.Name.Local)
				}
			}
		case xml.Comment:
			if text := string(t); edit(text) != text {
				edits = append(edits, xmlEdit{start: start, end: end, text: withheldComment})
			}
		case xml.ProcInst:
			if text := string(t.Inst); edit(text) != text {
				edits = append(edits, xmlEdit{start: start, end: end, text: withheldComment})
			}
		case xml.Directive:
			if text := string(t); edit(text) != text {
				return "", errors.New("a directive holds a value masking replaces, which it cannot replace in place")
			}
		}
	}
	w := xmlWalk{doc: doc, edits: edits}
	out := w.apply()
	if err := wellFormed(out); err != nil {
		return "", fmt.Errorf("masking produced a document that does not read back: %w", err)
	}
	return out, nil
}

// looksXML reports whether text opens like an XML document.
func looksXML(text string) bool {
	return strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), "<")
}
