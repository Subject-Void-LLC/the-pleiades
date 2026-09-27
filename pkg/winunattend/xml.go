// The small XML tree an answer file is built from, so every element comes
// out in the order Windows Setup reads it and every value is escaped by
// the standard library rather than spliced into text.
package winunattend

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// node is one element: its name, its attributes in order, and either text
// or child elements.
type node struct {
	name     string
	attrs    [][2]string
	text     string
	children []*node
}

// el is an element holding children.
func el(name string, children ...*node) *node {
	return &node{name: name, children: children}
}

// leaf is an element holding text.
func leaf(name, text string) *node {
	return &node{name: name, text: text}
}

// with adds an attribute and returns n.
func (n *node) with(name, value string) *node {
	n.attrs = append(n.attrs, [2]string{name, value})
	return n
}

// added marks n as an entry of a list, as every list entry in an answer
// file is marked.
func (n *node) added() *node {
	return n.with("wcm:action", "add")
}

// component is one of Windows' configurable components, for 64-bit
// Windows, with the attributes every answer file gives one.
func component(name string, children ...*node) *node {
	return el("component", children...).
		with("name", name).
		with("processorArchitecture", "amd64").
		with("publicKeyToken", "31bf3856ad364e35").
		with("language", "neutral").
		with("versionScope", "nonSxS").
		with("xmlns:wcm", "http://schemas.microsoft.com/WMIConfig/2002/State").
		with("xmlns:xsi", "http://www.w3.org/2001/XMLSchema-instance")
}

// pass is one configuration pass's settings.
func pass(name string, components ...*node) *node {
	return el("settings", components...).with("pass", name)
}

// document writes the answer file holding passes.
func document(passes ...*node) []byte {
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n")
	write(&b, el("unattend", passes...).with("xmlns", "urn:schemas-microsoft-com:unattend"), 0)
	return b.Bytes()
}

// write writes n and its children, two spaces of indent a level.
func write(b *bytes.Buffer, n *node, depth int) {
	indent := strings.Repeat("  ", depth)
	b.WriteString(indent + "<" + n.name)
	for _, a := range n.attrs {
		b.WriteString(" " + a[0] + "=\"")
		escape(b, a[1])
		b.WriteString("\"")
	}
	if len(n.children) == 0 {
		b.WriteString(">")
		escape(b, n.text)
		b.WriteString("</" + n.name + ">\n")
		return
	}
	b.WriteString(">\n")
	for _, c := range n.children {
		write(b, c, depth+1)
	}
	b.WriteString(indent + "</" + n.name + ">\n")
}

// escape writes s as XML text or an attribute value. Validation has
// already refused anything XML cannot carry, which EscapeText would
// otherwise replace rather than refuse.
func escape(b *bytes.Buffer, s string) {
	_ = xml.EscapeText(b, []byte(s)) // a bytes.Buffer does not fail
}
