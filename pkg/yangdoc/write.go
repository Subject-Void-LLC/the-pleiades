// Package yangdoc: writing elements back as XML on one line, for a document
// a client sends: a node read from a server, or one built step by step around
// nodes read from one.
package yangdoc

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// NetconfBase is RFC 6241's base namespace, the namespace of edit-config's
// operation attribute.
const NetconfBase = "urn:ietf:params:xml:ns:netconf:base:1.0"

// knownPrefixes are the prefixes Write gives the namespaces of the attributes
// it writes, so an operation attribute reads as nc:operation.
var knownPrefixes = map[string]string{NetconfBase: "nc"}

// New is an element built rather than read: name, with attrs, the given
// children and, for a leaf, text. A child read from a document keeps the
// document it came from, so the prefixes its value names still resolve.
func New(name xml.Name, attrs []xml.Attr, text string, children ...*Element) *Element {
	return &Element{Name: name, Attrs: attrs, text: text, Children: children}
}

// Write writes els as XML on one line: each element's namespace declared
// where it differs from the element around it, every namespace an attribute
// is in declared and given a prefix, every prefix a leaf's value names (an
// identityref) declared as the document the leaf came from binds it, and all
// text escaped, line breaks included. An attribute in a namespace this
// writer has no prefix for is refused.
func Write(els []*Element) (string, error) {
	var b strings.Builder
	for _, e := range els {
		if err := writeElement(&b, e, "", map[string]string{}); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// writeElement writes e to b, where the output so far has defaultNS as its
// default namespace and prefixes declared prefix to namespace.
func writeElement(b *strings.Builder, e *Element, defaultNS string, prefixes map[string]string) error {
	b.WriteString("<" + e.Name.Local)
	if e.Name.Space != defaultNS {
		b.WriteString(` xmlns="` + escaped(e.Name.Space) + `"`)
		defaultNS = e.Name.Space
	}
	scoped := prefixes
	declare := func(prefix, uri string) {
		if scoped[prefix] == uri {
			return
		}
		if len(scoped) == len(prefixes) {
			// Copied on the first declaration, so a sibling never sees a
			// prefix only this element declared.
			scoped = make(map[string]string, len(prefixes)+1)
			for k, v := range prefixes {
				scoped[k] = v
			}
		}
		scoped[prefix] = uri
		b.WriteString(" xmlns:" + prefix + `="` + escaped(uri) + `"`)
	}
	attrs := append([]xml.Attr(nil), e.Attrs...)
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name.Local < attrs[j].Name.Local })
	for _, a := range attrs {
		name := a.Name.Local
		if a.Name.Space != "" {
			prefix, ok := knownPrefixes[a.Name.Space]
			if !ok {
				return fmt.Errorf("element %s has an attribute in namespace %s, which this writer has no prefix for", e.Name.Local, a.Name.Space)
			}
			declare(prefix, a.Name.Space)
			name = prefix + ":" + name
		}
		b.WriteString(" " + name + `="` + escaped(a.Value) + `"`)
	}
	if e.IsLeaf() {
		// Every prefix a value names that its document binds (an
		// identityref's one, an instance-identifier's one per step) keeps
		// that binding; one nothing binds is text holding a colon (a time,
		// an address) and is written as it is.
		for _, pu := range e.ValuePrefixes() {
			declare(pu[0], pu[1])
		}
		b.WriteString(">" + escaped(e.text) + "</" + e.Name.Local + ">")
		return nil
	}
	b.WriteString(">")
	for _, c := range e.Children {
		if err := writeElement(b, c, defaultNS, scoped); err != nil {
			return err
		}
	}
	b.WriteString("</" + e.Name.Local + ">")
	return nil
}

// escaped is s as XML text or an attribute value: markup, quotes and every
// line break and tab escaped, so the result stays on one line.
func escaped(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s)) // #nosec G104 -- strings.Builder writes never fail
	return b.String()
}
