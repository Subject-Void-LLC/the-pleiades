// Package yangdoc reads YANG instance documents: the XML (RFC 7950 section 7)
// and the JSON (RFC 7951) a NETCONF or RESTCONF server answers with or a
// runbook writes. It gives a tree of elements with their namespaces resolved,
// the canonical form two reads of the same data share (so a method tells a
// change from none by comparing reads rather than assuming a write changed
// something), a writer that sends an element back on one line, and the checks
// a document a client sends is held to.
//
// It holds no YANG schema, so it never knows which element is a list entry or
// which leaf is a list's key, and nothing here guesses either. A caller that
// needs to know reasons from what the documents themselves prove.
package yangdoc

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// MaxDepth bounds the element nesting this package follows, as pkg/netcfg
// bounds its own walk of a device's XML.
const MaxDepth = 256

// Element is one XML element of a YANG instance document, as Parse read it.
type Element struct {
	// Name is the element's resolved namespace and local name.
	Name xml.Name

	// Attrs are its attributes other than namespace declarations, each
	// name's namespace resolved.
	Attrs []xml.Attr

	// Children are its child elements, in document order.
	Children []*Element

	// text is its character data, exactly as sent, when it has no child
	// element (a leaf, or a leaf-list entry); empty otherwise.
	text string

	// parent is the element it sits in, nil at the top of a document. A
	// copied element keeps it, so a prefix its value names still resolves
	// through the declarations of the document it came from.
	parent *Element

	// decls are the namespace prefixes declared on this element, prefix to
	// namespace, "" being the default namespace.
	decls map[string]string
}

// Text is the element's character data exactly as sent, for an element with
// no child element; empty for one with children.
func (e *Element) Text() string { return e.text }

// IsLeaf reports whether the element has no child element: a leaf, a
// leaf-list entry, or an empty element.
func (e *Element) IsLeaf() bool { return len(e.Children) == 0 }

// Find returns e's children named name (namespace and local name), in
// document order.
func (e *Element) Find(name xml.Name) []*Element {
	return FindIn(e.Children, name)
}

// FindIn returns the elements of list named name, in order.
func FindIn(list []*Element, name xml.Name) []*Element {
	var out []*Element
	for _, c := range list {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// LeadingLeaves are e's children up to its first child that has children of
// its own. RFC 7950 section 7.8.5 has a list entry's keys encoded first, in
// order, so for a list entry a server sends, its keys are among these.
func (e *Element) LeadingLeaves() []*Element {
	for i, c := range e.Children {
		if !c.IsLeaf() {
			return e.Children[:i]
		}
	}
	return e.Children
}

// Copy is e with attrs in place of its attributes and children in place of
// its children (the slice copied, so appending to one never writes into the
// other), keeping its name, its value and where it sits, so a prefix its
// value names still resolves through its own document.
func (e *Element) Copy(attrs []xml.Attr, children ...*Element) *Element {
	return &Element{Name: e.Name, Attrs: attrs, text: e.text, Children: append([]*Element(nil), children...), parent: e.parent, decls: e.decls}
}

// Resolve returns the namespace prefix names where e sits, through the
// declarations of e and the elements around it in its own document.
func (e *Element) Resolve(prefix string) (string, bool) {
	for at := e; at != nil; at = at.parent {
		if uri, ok := at.decls[prefix]; ok {
			return uri, true
		}
	}
	return "", false
}

// prefixedValue matches a value that may name a namespace prefix, as an
// identityref (ianaift:softwareLoopback) or an instance-identifier's first
// step does.
var prefixedValue = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*):(.+)$`)

// ValuePrefix returns the namespace prefix e's value names and what it is
// bound to where e sits, when its value starts with a prefix declared there.
func (e *Element) ValuePrefix() (prefix, uri string, ok bool) {
	m := prefixedValue.FindStringSubmatch(strings.TrimSpace(e.text))
	if m == nil || !e.IsLeaf() {
		return "", "", false
	}
	uri, ok = e.Resolve(m[1])
	return m[1], uri, ok
}

// Parse reads doc as a sequence of elements: one document, or the
// several top-level elements an edit-config's config holds. Comments,
// processing instructions and directives are skipped; white space between
// elements is set aside; text beside a child element (mixed content, which
// YANG data never holds) and text outside every element are refused, as is
// nesting past MaxDepth.
func Parse(doc []byte) ([]*Element, error) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var top []*Element
	var stack []*Element
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if len(stack) > 0 {
				return nil, errors.New("the document ends inside an element")
			}
			return top, nil
		}
		if err != nil {
			return nil, fmt.Errorf("the document is not well-formed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) >= MaxDepth {
				return nil, fmt.Errorf("the document nests deeper than %d elements", MaxDepth)
			}
			el := newElement(t)
			if n := len(stack); n > 0 {
				parent := stack[n-1]
				if strings.TrimSpace(parent.text) != "" {
					return nil, fmt.Errorf("element %s holds text beside a child element, which YANG data never does", parent.Name.Local)
				}
				parent.text = ""
				el.parent = parent
				parent.Children = append(parent.Children, el)
			} else {
				top = append(top, el)
			}
			stack = append(stack, el)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(t)) != "" {
					return nil, errors.New("the document holds text outside every element")
				}
				continue
			}
			cur := stack[len(stack)-1]
			if len(cur.Children) > 0 {
				if strings.TrimSpace(string(t)) != "" {
					return nil, fmt.Errorf("element %s holds text beside a child element, which YANG data never does", cur.Name.Local)
				}
				continue
			}
			cur.text += string(t)
		}
	}
}

// newElement is the element a start tag opens, its namespace declarations
// kept apart from its other attributes.
func newElement(t xml.StartElement) *Element {
	el := &Element{Name: t.Name}
	for _, a := range t.Attr {
		switch {
		case a.Name.Space == "xmlns":
			el.declare(a.Name.Local, a.Value)
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			el.declare("", a.Value)
		default:
			el.Attrs = append(el.Attrs, a)
		}
	}
	return el
}

// declare records one namespace declaration made on e.
func (e *Element) declare(prefix, uri string) {
	if e.decls == nil {
		e.decls = map[string]string{}
	}
	e.decls[prefix] = uri
}
