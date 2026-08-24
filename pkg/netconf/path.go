package netconf

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// This file is where caller-supplied values become XML, so it is where
// this package's injection surface lives, and the two halves are
// handled by two different mechanisms on purpose:
//
//   - Element and key NAMES are VALIDATED, not escaped. A name becomes
//     an XML tag, and there is no escaping that makes an arbitrary
//     string safe in that position: escaping "<" inside a tag name
//     produces a document that is well formed and addresses something
//     nobody asked for. So a name must be an XML NCName and anything
//     else is refused, which is the same one-narrow-sufficient-mechanism
//     shape remoteexec.QuoteArg and Shell.WriteLine's line-terminator
//     refusal each take.
//   - Key and text VALUES are ESCAPED with encoding/xml's own
//     EscapeText. A value is character data, where escaping genuinely is
//     the complete answer.
//
// The configuration payload itself is neither: it arrives as XML by
// definition, so it cannot be escaped and its names cannot be
// validated. It is instead checked for well-formedness and for nesting
// depth before a single byte of it is written to the device, which is
// what checkWellFormed is for.

// validateName refuses anything that is not an XML NCName (XML
// Namespaces 1.0 section 3): a letter or underscore, then letters,
// digits, periods, hyphens and underscores. Every YANG node name is an
// NCName, so nothing legitimate is refused, and nothing that could
// close a tag, open an attribute or introduce a namespace prefix is
// accepted.
//
// The check is ASCII-only, which is deliberately narrower than the XML
// specification's own definition. NCName admits a large range of
// non-ASCII letters; no YANG module in practice uses them, and
// accepting them would mean carrying Unicode category tables to
// distinguish a letter from a combining character for a case that does
// not arise. A refusal here names the offending value, so a legitimate
// use that this rejected would be reported rather than silently
// mangled.
func validateName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("netconf: empty %s name", kind)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
			continue
		case i > 0 && (c >= '0' && c <= '9' || c == '.' || c == '-'):
			continue
		}
		return fmt.Errorf("netconf: %s name %q contains %q, which is not valid in an XML element name: a name must be a letter or underscore followed by letters, digits, periods, hyphens or underscores",
			kind, name, string(c))
	}
	return nil
}

// escape renders s as XML character data.
func escape(s string) string {
	var b bytes.Buffer
	// The error is unreachable: bytes.Buffer's Write never fails.
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// subtreeFilter renders p as an RFC 6241 section 6 subtree filter, or
// the empty string when p addresses the whole datastore.
func subtreeFilter(p datastore.Path) (string, error) {
	if p.IsRoot() {
		return "", nil
	}
	inner, err := nestElements(p, "", "")
	if err != nil {
		return "", err
	}
	return `<filter type="subtree">` + inner + `</filter>`, nil
}

// wrapInPath nests inner inside the elements p names, so a payload
// describing a leaf arrives at the device inside the containers that
// address it.
//
// When p is not the root, the innermost wrapper carries an explicit
// nc:operation attribute and edit-config's default-operation is set to
// "none" (see SetConfig). That pairing is not stylistic. Setting
// default-operation to "replace" while the payload is wrapped in
// ancestor containers would apply replace semantics TO THOSE
// CONTAINERS, so replacing one interface's configuration would replace
// every interface. Scoping the operation to the innermost element and
// telling the device to do nothing by default is the only construction
// that means what the caller asked for.
func wrapInPath(p datastore.Path, inner string, op datastore.Operation) (string, error) {
	if p.IsRoot() {
		return inner, nil
	}
	return nestElements(p, inner, op.String())
}

// nestElements builds p's elements as nested XML, placing inner at the
// deepest level and, when opAttr is non-empty, an nc:operation
// attribute on that same deepest element.
func nestElements(p datastore.Path, inner, opAttr string) (string, error) {
	var opens []string
	var closes []string

	for i, elem := range p.Elem {
		if err := validateName("element", elem.Name); err != nil {
			return "", err
		}
		var attrs strings.Builder
		if elem.Namespace != "" {
			fmt.Fprintf(&attrs, " xmlns=%q", escape(elem.Namespace))
		}
		if i == len(p.Elem)-1 && opAttr != "" {
			fmt.Fprintf(&attrs, " xmlns:nc=%q nc:operation=%q", baseNamespace, opAttr)
		}

		var keys strings.Builder
		for _, name := range sortedKeys(elem.Keys) {
			if err := validateName("list key", name); err != nil {
				return "", err
			}
			fmt.Fprintf(&keys, "<%s>%s</%s>", name, escape(elem.Keys[name]), name)
		}

		opens = append(opens, fmt.Sprintf("<%s%s>%s", elem.Name, attrs.String(), keys.String()))
		closes = append(closes, fmt.Sprintf("</%s>", elem.Name))
	}

	var b strings.Builder
	for _, o := range opens {
		b.WriteString(o)
	}
	b.WriteString(inner)
	for i := len(closes) - 1; i >= 0; i-- {
		b.WriteString(closes[i])
	}
	return b.String(), nil
}

// sortedKeys returns a map's keys in a stable order, so the same Path
// always renders to the same bytes. Without it, two identical requests
// would differ on the wire between runs, which would make a captured
// fixture untestable and a diff of two runs unreadable.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// checkDepth walks raw's tokens and refuses a document nesting deeper
// than max.
//
// This bound is this package's own and is not redundant with the byte
// bound, which is the part worth spelling out. encoding/xml enforces a
// depth limit only on subtrees it unmarshals INTO A STRUCT FIELD; a
// subtree captured as raw inner XML, which is exactly how this client
// carries configuration data, is skipped rather than counted, and
// streaming through Decoder.Token bounds nothing at all. Meanwhile the
// tokenizer keeps a heap-allocated entry per open element, so a 64 MiB
// document of nothing but "<a>" declares roughly sixteen million of
// them and costs several times its own size in memory. The byte bound
// alone therefore does not bound memory; this does.
//
// Go's encoding/xml does not expand DTD-declared entities at all, so
// the classic billion-laughs and external-entity documents are already
// refused by the parser with a character-entity error rather than by
// anything here. Depth is the exposure that remains.
func checkDepth(raw []byte, max int) error {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("malformed XML: %w", err)
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
			if depth > max {
				return fmt.Errorf("XML nests more than %d elements deep", max)
			}
		case xml.EndElement:
			depth--
		}
	}
}

// checkWellFormed proves a caller-supplied configuration payload parses
// and respects the depth bound before any of it is written to a device.
//
// Doing this locally rather than letting the device object is not
// belt-and-braces. A payload that is malformed part way through is a
// payload the device has already begun applying when it finds out,
// which on a device with no candidate datastore (Cisco IOS XE, for one)
// means a live configuration in a state nobody described. Catching it
// before the first byte goes out is the only point at which it costs
// nothing.
//
// A DOCTYPE declaration is refused outright. Go's parser would not
// expand its entities anyway, so this is not the mitigation for
// billion-laughs; it is refused because a document type declaration has
// no legitimate place in a YANG configuration fragment, and accepting
// something inert-but-meaningless is how a parser change later becomes a
// vulnerability.
func checkWellFormed(raw []byte, maxDepth int) error {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth := 0
	elements := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("malformed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			elements++
			if depth > maxDepth {
				return fmt.Errorf("XML nests more than %d elements deep", maxDepth)
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(string(t))), "DOCTYPE") {
				return fmt.Errorf("XML carries a DOCTYPE declaration, which has no place in a configuration payload")
			}
		}
	}
	// There is deliberately no "depth != 0 means unclosed elements"
	// check here. It would be unreachable: verified by execution on this
	// module's toolchain, encoding/xml reports an unclosed element as
	// "XML syntax error on line N: unexpected EOF" from Token itself and
	// never reaches io.EOF, so the loop above has already returned. A
	// second, unreachable guard would read as though it covered
	// something.
	if elements == 0 {
		return fmt.Errorf("no XML elements found")
	}
	return nil
}
