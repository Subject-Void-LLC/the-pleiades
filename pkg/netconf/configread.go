// Package netconf: reading the configuration an edit touches, so a caller can
// tell what an edit changed from two reads rather than assuming the edit
// changed something. EditFilter derives a subtree filter from the edit's own
// content; ReadConfig reads through it, in RFC 6243's explicit with-defaults
// mode where the server offers it, and returns the reply's data read whole,
// every namespace resolved through the reply's own declarations.
package netconf

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// CapabilityWithDefaults is RFC 6243's with-defaults capability, whose
// parameters name the server's basic mode and the modes it also supports.
const CapabilityWithDefaults = "urn:ietf:params:netconf:capability:with-defaults:1.0"

// withDefaultsNamespace is the namespace of get-config's with-defaults leaf.
const withDefaultsNamespace = "urn:ietf:params:xml:ns:yang:ietf-netconf-with-defaults"

// WithDefaultsExplicit is RFC 6243's explicit mode: a leaf set by a client is
// reported, even to its default value, and a leaf left at its default is not.
const WithDefaultsExplicit = "explicit"

// WithDefaults is the with-defaults mode ReadConfig reads in: explicit when
// the server's basic mode is explicit or it lists explicit among the modes it
// also supports (then asked for by name), otherwise the basic mode it
// advertises, and "" when it advertises no with-defaults capability at all,
// which RFC 6243 leaves undefined.
func (s *Session) WithDefaults() string {
	basic, also := s.withDefaultsModes()
	if basic == WithDefaultsExplicit || also {
		return WithDefaultsExplicit
	}
	return basic
}

// withDefaultsModes are the basic mode the server's with-defaults capability
// names, and whether its also-supported list holds explicit.
func (s *Session) withDefaultsModes() (basic string, alsoExplicit bool) {
	for _, urn := range s.serverCapabilities {
		base, query, _ := strings.Cut(urn, "?")
		if base != CapabilityWithDefaults {
			continue
		}
		values, err := url.ParseQuery(query)
		if err != nil {
			return "", false
		}
		for _, m := range strings.Split(values.Get("also-supported"), ",") {
			if strings.TrimSpace(m) == WithDefaultsExplicit {
				alsoExplicit = true
			}
		}
		return values.Get("basic-mode"), alsoExplicit
	}
	return "", false
}

// ReadConfig reads source through filter, the contents of a subtree filter
// (EditFilter writes one), or the whole datastore when filter is empty. It
// asks for with-defaults explicit when the server supports it beside another
// basic mode, and returns the elements of the reply's data, read from the
// whole reply so a prefix the server declared outside data still resolves.
func (s *Session) ReadConfig(ctx context.Context, source Datastore, filter string) ([]*yangdoc.Element, error) {
	var body strings.Builder
	fmt.Fprintf(&body, "<get-config><source><%s/></source>", source)
	if filter != "" {
		body.WriteString(`<filter type="subtree">` + filter + `</filter>`)
	}
	if basic, also := s.withDefaultsModes(); basic != WithDefaultsExplicit && also {
		fmt.Fprintf(&body, "<with-defaults xmlns=%q>%s</with-defaults>", withDefaultsNamespace, WithDefaultsExplicit)
	}
	body.WriteString("</get-config>")
	reply, err := s.do(ctx, body.String())
	if err != nil {
		return nil, err
	}
	if reply.Data == nil {
		return nil, fmt.Errorf("netconf: the server's reply to get-config of %s carried no <data> element", source)
	}
	els, err := yangdoc.Parse(reply.raw)
	if err != nil {
		return nil, fmt.Errorf("netconf: reading the reply to get-config of %s: %w", source, err)
	}
	for _, top := range els {
		for _, data := range top.Find(xml.Name{Space: baseNamespace, Local: "data"}) {
			return data.Children, nil
		}
	}
	return nil, fmt.Errorf("netconf: the server's reply to get-config of %s carried no <data> element in the base namespace", source)
}

// operationAttr is edit-config's operation attribute.
var operationAttr = xml.Name{Space: baseNamespace, Local: "operation"}

// EditFilter is the contents of a subtree filter selecting everything content
// can change, for reading it before and after the edit, or "" (the whole
// datastore) when replaceAll says edit-config's default-operation is replace,
// which RFC 6241 section 7.2 defines as replacing the whole datastore.
//
// Each top-level element of content is followed down by name while it holds
// only elements that hold elements, carries no attribute, and so can only be
// a container (a list entry holds its keys, which are leaves); the first
// element that holds a leaf, carries an attribute (an operation) or is a leaf
// itself is selected whole, every instance of its name under the elements
// above it. No value from content narrows the selection, since without the
// schema no leaf is known to be a key: the read is a superset of what the
// edit can touch, never a subset.
func EditFilter(content string, replaceAll bool) (string, error) {
	els, err := yangdoc.Parse([]byte(content))
	if err != nil {
		return "", fmt.Errorf("netconf: content: %w", err)
	}
	if len(els) == 0 {
		return "", errors.New("netconf: content holds no element")
	}
	if replaceAll {
		return "", nil
	}
	root := &selection{}
	for _, e := range els {
		root.add(e)
	}
	var b strings.Builder
	for _, k := range root.kids {
		k.write(&b, "")
	}
	return b.String(), nil
}

// Descendable reports whether e can only be a container, which a filter or
// a reader follows by name: it holds elements only, each of which holds
// elements, and it carries no attribute. Anything else is selected whole.
func Descendable(e *yangdoc.Element) bool {
	if len(e.Attrs) > 0 || e.IsLeaf() {
		return false
	}
	for _, c := range e.Children {
		if c.IsLeaf() {
			return false
		}
	}
	return true
}

// selection is one node of a subtree filter being built: an element followed
// by name, or one selected whole.
type selection struct {
	name xml.Name
	all  bool
	kids []*selection
}

// add selects what e can change under s.
func (s *selection) add(e *yangdoc.Element) {
	c := s.child(e.Name)
	if c.all {
		return
	}
	if !Descendable(e) {
		c.all, c.kids = true, nil
		return
	}
	for _, k := range e.Children {
		c.add(k)
	}
}

// child is s's child named name, added when it has none.
func (s *selection) child(name xml.Name) *selection {
	for _, k := range s.kids {
		if k.name == name {
			return k
		}
	}
	k := &selection{name: name}
	s.kids = append(s.kids, k)
	return k
}

// write writes s to b as subtree filter XML, under an element whose
// namespace is ns.
func (s *selection) write(b *strings.Builder, ns string) {
	b.WriteString("<" + s.name.Local)
	if s.name.Space != ns {
		b.WriteString(` xmlns="` + escape(s.name.Space) + `"`)
	}
	if s.all || len(s.kids) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteString(">")
	for _, k := range s.kids {
		k.write(b, s.name.Space)
	}
	b.WriteString("</" + s.name.Local + ">")
}

// Operation is the operation attribute e carries, "" for none.
func Operation(e *yangdoc.Element) string {
	for _, a := range e.Attrs {
		if a.Name == operationAttr {
			return a.Value
		}
	}
	return ""
}
