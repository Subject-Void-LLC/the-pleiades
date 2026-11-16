// Package yangdoc: the canonical form two reads of the same data share, so a
// method tells a change from none by comparing the configuration before and
// after a write rather than assuming the write changed it, and the checks a
// document a client sends is held to. JSON keeps its numbers as written with
// its object keys sorted; XML has each element's namespace resolved, its
// attributes sorted, the white space between elements set aside, and a value
// naming a namespace prefix (an identityref) read by the namespace it names,
// since a server picks its own prefixes. Order is kept everywhere else: the
// order of a YANG ordered-by user list is part of the configuration, and a
// schema is what would say which lists are not.
package yangdoc

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

// Canonical is doc in its comparable form, read as JSON or as XML.
func Canonical(doc []byte, asJSON bool) (string, error) {
	if asJSON {
		return canonJSON(doc)
	}
	els, err := Parse(doc)
	if err != nil {
		return "", fmt.Errorf("the answer is not usable XML: %w", err)
	}
	return CanonicalElements(els), nil
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

// CanonicalElements is els in their comparable form: each element by its
// resolved namespace and name with its attributes sorted, then its value
// (trimmed, a leading prefix read by its namespace) or its children in order.
func CanonicalElements(els []*Element) string {
	var b strings.Builder
	for _, e := range els {
		writeCanonical(&b, e)
	}
	return b.String()
}

// writeCanonical writes one element's canonical form to b.
func writeCanonical(b *strings.Builder, e *Element) {
	fmt.Fprintf(b, "<{%s}%s", e.Name.Space, e.Name.Local)
	attrs := make([]string, 0, len(e.Attrs))
	for _, a := range e.Attrs {
		attrs = append(attrs, fmt.Sprintf("{%s}%s=%q", a.Name.Space, a.Name.Local, a.Value))
	}
	sort.Strings(attrs)
	for _, a := range attrs {
		b.WriteString(" " + a)
	}
	b.WriteString(">")
	if e.IsLeaf() {
		if text := CanonicalValue(e); text != "" {
			fmt.Fprintf(b, "%q", text)
		}
	}
	for _, c := range e.Children {
		writeCanonical(b, c)
	}
	b.WriteString("</>")
}

// CanonicalValue is a leaf's value as two reads compare it: trimmed, and a
// leading namespace prefix declared where it sits replaced by the namespace
// it names.
func CanonicalValue(e *Element) string {
	text := strings.TrimSpace(e.text)
	if _, uri, ok := e.ValuePrefix(); ok {
		_, rest, _ := strings.Cut(text, ":")
		return "{" + uri + "}" + rest
	}
	return text
}

// CheckDocument refuses content a server would read as something other than
// one document of its format: JSON that does not parse, and XML that is not
// well-formed, holds no element, holds a DOCTYPE or another directive (a
// server's parser may expand an entity Go's does not), or nests past
// MaxDepth. XML may hold several top-level elements, as an edit-config's
// config does.
func CheckDocument(content string, asJSON bool) error {
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
			if depth > MaxDepth {
				return fmt.Errorf("content nests deeper than %d elements", MaxDepth)
			}
		case xml.EndElement:
			depth--
		}
	}
}
