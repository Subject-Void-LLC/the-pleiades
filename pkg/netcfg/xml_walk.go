// Package netcfg: the token-by-token half of MaskXML, which decides what
// each element, attribute, comment and directive of a device's XML
// configuration does to the masked copy.
package netcfg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// newDecoder is a strict decoder over doc: an undefined entity, invalid
// UTF-8 or a declared encoding other than UTF-8 (there is no CharsetReader)
// is an error rather than a guess.
func newDecoder(doc string) *xml.Decoder {
	d := xml.NewDecoder(strings.NewReader(doc))
	d.Strict = true
	return d
}

// xmlError is a decoder's error with only its line number kept: a syntax
// error's own message can quote the document (an undefined entity's name),
// and the document is a device's configuration.
func xmlError(err error) error {
	var se *xml.SyntaxError
	if errors.As(err, &se) {
		return fmt.Errorf("the document is not well-formed XML at line %d", se.Line)
	}
	return errors.New("the document could not be read as XML")
}

// wellFormed reports whether doc reads to its end as XML, the check MaskXML
// makes of its own output before returning it.
func wellFormed(doc string) error {
	d := newDecoder(doc)
	for {
		if _, err := d.Token(); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return xmlError(err)
		}
	}
}

// token applies one token, which spans doc[start:end], to the walk.
func (w *xmlWalk) token(tok xml.Token, start, end int) error {
	var top *xmlFrame
	if n := len(w.stack); n > 0 {
		top = w.stack[n-1]
	}
	switch t := tok.(type) {
	case xml.StartElement:
		return w.open(t, top)
	case xml.EndElement:
		f := w.stack[len(w.stack)-1]
		w.stack = w.stack[:len(w.stack)-1]
		var parent *xmlFrame
		if n := len(w.stack); n > 0 {
			parent = w.stack[n-1]
		}
		return w.leafText(f, parent)
	case xml.CharData:
		if top == nil {
			// Text between top-level elements belongs to no leaf: it is read
			// as any other leaf's text is, on its own.
			f := &xmlFrame{local: "top-level text"}
			f.spans = [][2]int{{start, end}}
			f.text.Write(t)
			return w.leafText(f, nil)
		}
		top.spans = append(top.spans, [2]int{start, end})
		top.text.Write(t)
	case xml.Comment:
		w.edits = append(w.edits, xmlEdit{start: start, end: end, text: withheldComment})
	case xml.ProcInst:
		// The XML declaration is kept; any other instruction is free text.
		if t.Target != "xml" {
			w.edits = append(w.edits, xmlEdit{start: start, end: end, text: withheldComment})
		}
	case xml.Directive:
		// A DOCTYPE can declare entities and a NETCONF reply has no use for
		// one (RFC 6241 section 3 forbids it).
		return errors.New("the document holds a DOCTYPE or another directive, which a configuration never needs")
	}
	return nil
}

// open pushes a start element's frame, refusing an attribute that may hold
// a secret: an attribute's value cannot be masked in place, so the whole
// document is refused instead.
func (w *xmlWalk) open(t xml.StartElement, parent *xmlFrame) error {
	if len(w.stack) >= MaxXMLDepth {
		return fmt.Errorf("the document nests more than %d elements deep", MaxXMLDepth)
	}
	local := strings.ToLower(t.Name.Local)
	inSNMP := strings.Contains(local, "snmp")
	if parent != nil {
		parent.hasChild = true
		inSNMP = inSNMP || parent.inSNMP
	}
	for _, a := range t.Attr {
		if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
			// A namespace declaration's value is the namespace's URI.
			continue
		}
		if secretName(a.Name.Local, inSNMP) || cryptShaped.MatchString(strings.TrimSpace(a.Value)) {
			return fmt.Errorf("a <%s> element's %q attribute may hold a secret, which masking cannot replace in place", local, a.Name.Local)
		}
		for _, k := range w.known {
			if strings.Contains(a.Value, k) {
				return fmt.Errorf("a <%s> element's %q attribute holds the run's own credential", local, a.Name.Local)
			}
		}
	}
	f := &xmlFrame{local: local, secret: secretName(local, inSNMP), inSNMP: inSNMP}
	// Secrecy passes from a secret-named element to every descendant, and a
	// metadata-named element (a lifetime, an algorithm) stops it there.
	f.inSecret = parent != nil && (parent.secret || parent.inSecret) && !metadataNames[local]
	w.stack = append(w.stack, f)
	return nil
}

// escapeXMLText escapes text for an element's content: the three characters
// that would otherwise read as markup, and a carriage return, which a
// decoder would otherwise fold into the newline after it.
func escapeXMLText(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;").Replace(text)
}
