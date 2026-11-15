// Package restconf: reading a document a device answered in a form two reads
// of the same data share, so net.restconf.config tells a change from none by
// comparing the resource before and after a write rather than assuming the
// write changed it: JSON with its object keys sorted and its numbers as
// written, XML with each element's namespace resolved, its attributes sorted
// and white space between elements set aside. Order within a list is kept,
// since it can be meaningful (a YANG ordered-by user list).
package restconf

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// maxCanonDepth bounds the nesting canonXML follows, as pkg/netcfg bounds
// its own walk of a device's XML.
const maxCanonDepth = 256

// canonical is doc in its comparable form, read as JSON or XML.
func canonical(doc []byte, asJSON bool) (string, error) {
	if asJSON {
		return canonJSON(doc)
	}
	return canonXML(doc)
}

// canonJSON is doc decoded and encoded again: encoding/json writes an
// object's keys sorted, and UseNumber keeps each number as written.
func canonJSON(doc []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("the answer is not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("the answer holds more than one JSON value")
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// canonXML is doc as a sequence of elements and text: each element by its
// resolved namespace and name with its attributes sorted (namespace
// declarations left out, since a prefix is a spelling), each run of text
// trimmed and white space alone dropped, and comments, processing
// instructions and directives dropped.
func canonXML(doc []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var b strings.Builder
	depth := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return b.String(), nil
		}
		if err != nil {
			return "", fmt.Errorf("the answer is not well-formed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxCanonDepth {
				return "", fmt.Errorf("the answer nests deeper than %d elements", maxCanonDepth)
			}
			fmt.Fprintf(&b, "<{%s}%s", t.Name.Space, t.Name.Local)
			var attrs []string
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				attrs = append(attrs, fmt.Sprintf("{%s}%s=%q", a.Name.Space, a.Name.Local, a.Value))
			}
			sort.Strings(attrs)
			for _, a := range attrs {
				b.WriteString(" " + a)
			}
			b.WriteString(">")
		case xml.EndElement:
			depth--
			b.WriteString("</>")
		case xml.CharData:
			if text := strings.TrimSpace(string(t)); text != "" {
				fmt.Fprintf(&b, "%q", text)
			}
		}
	}
}

// checkDocument refuses content the device would read as something other
// than one document: JSON that does not parse, and XML that is not
// well-formed, holds a DOCTYPE or another directive (a device's parser may
// expand an entity Go's does not), or nests past maxCanonDepth.
func checkDocument(content string, asJSON bool) error {
	if asJSON {
		if _, err := canonJSON([]byte(content)); err != nil {
			return fmt.Errorf("content: %w", err)
		}
		return nil
	}
	dec := xml.NewDecoder(strings.NewReader(content))
	depth, elements := 0, 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if elements == 0 {
				return errors.New("content holds no XML element")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("content is not well-formed XML: %w", err)
		}
		switch tok.(type) {
		case xml.Directive:
			return errors.New("content holds a DOCTYPE or another directive, which is refused")
		case xml.StartElement:
			depth++
			elements++
			if depth > maxCanonDepth {
				return fmt.Errorf("content nests deeper than %d elements", maxCanonDepth)
			}
		case xml.EndElement:
			depth--
		}
	}
}
