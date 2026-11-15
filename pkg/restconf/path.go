// Package restconf: building a resource's path (RFC 8040 section 3.5.3).
// A path is a list of data nodes, each named by its YANG identifier and, when
// it is the first or its module differs from its parent's, by its module; a
// list entry carries its keys. Every key value is percent-encoded on its own,
// every byte but RFC 3986's unreserved ones, so a "/" or "," in a value
// (an interface named GigabitEthernet0/0/1, a description with a comma) stays
// inside its key and never addresses another resource. Nothing here
// concatenates caller text into a URL.
package restconf

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// The bounds on a path.
const (
	// MaxSegments is the most data nodes one path may name.
	MaxSegments = 128
	// MaxKeyBytes bounds one key value before it is escaped.
	MaxKeyBytes = 4096
)

// Segment is one data node of a resource path.
type Segment struct {
	// Module is the node's module name, required on a path's first node
	// and wherever the module differs from the parent's; empty to inherit.
	Module string
	// Name is the node's YANG identifier.
	Name string
	// Keys are a list entry's key values, in the order the list's key
	// statement names them, unescaped. nil names the node itself (a
	// container, a leaf, or a whole list).
	Keys []string
}

// Node is the data node module:name, or name alone when module is empty.
func Node(module, name string) Segment {
	return Segment{Module: module, Name: name}
}

// Entry is s naming the one list entry whose keys are keys. An entry needs
// at least one key; an empty value is a key whose value is the empty string.
func (s Segment) Entry(keys ...string) Segment {
	s.Keys = append(make([]string, 0, len(keys)), keys...)
	return s
}

// Path is a resource path under a datastore resource such as {+restconf}/data.
type Path []Segment

// Escaped returns p as RFC 8040's api-path, every key value escaped, with no
// leading slash: "ietf-interfaces:interfaces/interface=GigabitEthernet0%2F1".
// An empty path is "", the datastore resource itself. It refuses a node
// whose module or name is not a YANG identifier, a first node with no
// module, an entry with no key, a key past MaxKeyBytes, a key that is not
// UTF-8 or holds a control character, and more than MaxSegments nodes.
func (p Path) Escaped() (string, error) {
	if len(p) > MaxSegments {
		return "", fmt.Errorf("a RESTCONF path may name at most %d nodes", MaxSegments)
	}
	parts := make([]string, 0, len(p))
	for i, s := range p {
		part, err := s.escaped(i == 0)
		if err != nil {
			return "", fmt.Errorf("RESTCONF path node %d: %w", i+1, err)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "/"), nil
}

// escaped is one node as an api-identifier or a list-instance.
func (s Segment) escaped(first bool) (string, error) {
	if !yanglib.IsIdentifier(s.Name) {
		return "", fmt.Errorf("%q is not a YANG identifier", s.Name)
	}
	if s.Module == "" && first {
		return "", errors.New("the first node must name its module")
	}
	if s.Module != "" && !yanglib.IsIdentifier(s.Module) {
		return "", fmt.Errorf("module %q is not a YANG identifier", s.Module)
	}
	// An identifier holds only unreserved characters, so it is written as
	// it is.
	out := s.Name
	if s.Module != "" {
		out = s.Module + ":" + s.Name
	}
	if s.Keys == nil {
		return out, nil
	}
	if len(s.Keys) == 0 {
		return "", fmt.Errorf("list entry %s names no key", out)
	}
	keys := make([]string, 0, len(s.Keys))
	for _, k := range s.Keys {
		if err := checkKey(k); err != nil {
			return "", fmt.Errorf("a key of %s: %w", out, err)
		}
		keys = append(keys, EscapeKey(k))
	}
	return out + "=" + strings.Join(keys, ","), nil
}

// checkKey refuses a key value that is too long, is not UTF-8, or holds a
// control character: none is a value a YANG string can carry to a server
// that reads it the same way this client wrote it.
func checkKey(k string) error {
	switch {
	case len(k) > MaxKeyBytes:
		return fmt.Errorf("the value is longer than %d bytes", MaxKeyBytes)
	case !utf8.ValidString(k):
		return errors.New("the value is not UTF-8")
	case strings.ContainsFunc(k, func(r rune) bool { return r < 0x20 || (r >= 0x7f && r < 0xa0) }):
		return errors.New("the value holds a control character")
	}
	return nil
}

// EscapeKey percent-encodes every byte of v but RFC 3986's unreserved
// characters (letters, digits, "-", ".", "_" and "~"), uppercase hex: RFC
// 8040 section 3.5.3.1 requires every reserved character, the comma among
// them, to be encoded in a key value, and encoding the rest too means no
// server's reading of a sub-delimiter can move a key.
func EscapeKey(v string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		if unreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// unreserved reports whether c is RFC 3986 section 2.3's unreserved.
func unreserved(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}
