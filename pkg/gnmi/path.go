// Package gnmi: a path, the address of a node in a device's data tree, as
// gNMI writes it (gNMI path conventions): an optional origin, then elements
// from the root, each a name and, for a list entry, its keys. A path is
// built from parts and checked before it is sent: every name is a YANG
// identifier (with an optional module prefix), every key value is text with
// no control character, and the bounds below hold, so a value a person or a
// device supplied can never become another element of the path.
package gnmi

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	gpb "github.com/openconfig/gnmi/proto/gnmi"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// The bounds on a path.
const (
	// MaxElems is the most elements one path may name.
	MaxElems = 128
	// MaxKeys is the most keys one element may carry.
	MaxKeys = 32
	// MaxKeyBytes bounds one key value.
	MaxKeyBytes = 4096
	// MaxPathBytes bounds a path as written, before it is parsed.
	MaxPathBytes = 64 << 10
	// maxOriginBytes bounds an origin.
	maxOriginBytes = 64
)

// The wildcards a read path may hold: "*" for any one element or key value,
// "..." for any number of elements (gNMI path conventions, section 2.6).
const (
	WildcardAny   = "*"
	WildcardDeep  = "..."
	wildcardValue = "*"
)

// originPattern is an origin's grammar: a name such as openconfig, rfc7951
// or cli, never anything that could carry a path.
var originPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Elem is one element of a path.
type Elem struct {
	// Name is the node's YANG identifier, optionally prefixed by its module
	// ("openconfig-interfaces:interfaces"), or a wildcard on a read path.
	Name string
	// Keys are a list entry's keys, by key name; nil for a container, a
	// leaf or a whole list.
	Keys map[string]string
}

// Path addresses a node of a device's data tree. The zero Path is the root.
type Path struct {
	// Origin names the schema the path is in ("openconfig", "rfc7951",
	// "cli"), empty for the device's default.
	Origin string
	// Elems are the path's elements from the root, outermost first.
	Elems []Elem
}

// ParsePath reads s, a path as gNMI's path conventions write it, with an
// optional origin before ":/": "/interfaces/interface[name=eth0]/config" or
// "openconfig:/system/config/hostname". A key value holding "]" or "\" writes
// it as "\]" or "\\"; every other character stands for itself, "/" and "="
// included. An empty path, an empty element ("a//b"), an unterminated key, a
// key without "=", a repeated key, an unknown escape, a name that is not an
// identifier and a value holding a control character are each refused.
// Wildcards are read here and refused where a path names one node (Set).
func ParsePath(s string) (Path, error) {
	var p Path
	if s == "" {
		return p, errors.New("the path is empty")
	}
	if len(s) > MaxPathBytes {
		return p, fmt.Errorf("the path is longer than %d bytes", MaxPathBytes)
	}
	rest := s
	if !strings.HasPrefix(s, "/") {
		i := strings.Index(s, ":/")
		if i <= 0 {
			return p, errors.New("a path starts with / or with an origin and :/ (openconfig:/system)")
		}
		p.Origin, rest = s[:i], s[i+1:]
	}
	rest = rest[1:]
	for rest != "" {
		elem, remaining, err := parseElem(rest)
		if err != nil {
			return Path{}, err
		}
		p.Elems = append(p.Elems, elem)
		if remaining == "" {
			break
		}
		// parseElem stops only at "/" or the end.
		rest = remaining[1:]
		if rest == "" {
			return Path{}, errors.New("the path ends with /, an empty element")
		}
	}
	if err := p.Validate(true); err != nil {
		return Path{}, err
	}
	return p, nil
}

// parseElem reads one element from the front of s, returning what follows
// it: "" or text starting with "/".
func parseElem(s string) (Elem, string, error) {
	end := strings.IndexAny(s, "/[]\\")
	if end < 0 {
		end = len(s)
	}
	elem := Elem{Name: s[:end]}
	if elem.Name == "" {
		return Elem{}, "", errors.New("the path holds an empty element")
	}
	s = s[end:]
	for strings.HasPrefix(s, "[") {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return Elem{}, "", fmt.Errorf("a key of %s has no =", quoted(elem.Name))
		}
		name := s[1:eq]
		value, rest, err := parseKeyValue(s[eq+1:])
		if err != nil {
			return Elem{}, "", fmt.Errorf("key %s of %s: %w", quoted(name), quoted(elem.Name), err)
		}
		if elem.Keys == nil {
			elem.Keys = map[string]string{}
		}
		if _, repeated := elem.Keys[name]; repeated {
			return Elem{}, "", fmt.Errorf("key %s of %s is given twice", quoted(name), quoted(elem.Name))
		}
		elem.Keys[name] = value
		s = rest
	}
	if s != "" && s[0] != '/' {
		return Elem{}, "", fmt.Errorf("element %s is followed by %q, not / or a key", quoted(elem.Name), s[:1])
	}
	return elem, s, nil
}

// parseKeyValue reads a key value up to its closing "]", undoing the two
// escapes a value may hold, and returns what follows the bracket.
func parseKeyValue(s string) (string, string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ']':
			return b.String(), s[i+1:], nil
		case '\\':
			if i+1 == len(s) || (s[i+1] != ']' && s[i+1] != '\\') {
				return "", "", errors.New(`an escape in a key value is \] or \\ and nothing else`)
			}
			i++
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", errors.New("the key value has no closing ]")
}

// Validate checks p against the rules a path sent to a device is held to,
// with wildcards allowed or not: the bounds, the origin's grammar, every name
// a YANG identifier with an optional module prefix, and every key value
// UTF-8 with no control character.
func (p Path) Validate(allowWildcards bool) error {
	if p.Origin != "" && (len(p.Origin) > maxOriginBytes || !originPattern.MatchString(p.Origin)) {
		return fmt.Errorf("origin %s is not a name", quoted(p.Origin))
	}
	if len(p.Elems) > MaxElems {
		return fmt.Errorf("the path names %d elements, more than %d", len(p.Elems), MaxElems)
	}
	for _, e := range p.Elems {
		if err := validName(e.Name, allowWildcards); err != nil {
			return err
		}
		if len(e.Keys) > MaxKeys {
			return fmt.Errorf("element %s carries %d keys, more than %d", e.Name, len(e.Keys), MaxKeys)
		}
		for name, value := range e.Keys {
			if err := validName(name, false); err != nil {
				return fmt.Errorf("a key of %s: %w", e.Name, err)
			}
			if err := validKeyValue(value, allowWildcards); err != nil {
				return fmt.Errorf("key %s of %s: %w", name, e.Name, err)
			}
		}
	}
	return nil
}

// validName checks a name: a YANG identifier, optionally prefixed by a module
// identifier and ":", or, when allowed, a wildcard.
func validName(name string, allowWildcards bool) error {
	if name == WildcardAny || name == WildcardDeep {
		if !allowWildcards {
			return fmt.Errorf("the wildcard %s names no one node, and this path must", name)
		}
		return nil
	}
	local := name
	if prefix, after, prefixed := strings.Cut(name, ":"); prefixed {
		if !yanglib.IsIdentifier(prefix) {
			return fmt.Errorf("%s is not a YANG identifier with a module prefix", quoted(name))
		}
		local = after
	}
	if !yanglib.IsIdentifier(local) {
		return fmt.Errorf("%s is not a YANG identifier", quoted(name))
	}
	return nil
}

// validKeyValue checks a key value: bounded UTF-8 with no control or
// invisible formatting character, and the wildcard only where allowed.
func validKeyValue(v string, allowWildcards bool) error {
	if v == wildcardValue && !allowWildcards {
		return errors.New("the wildcard * names no one entry, and this path must")
	}
	if len(v) > MaxKeyBytes {
		return fmt.Errorf("the value is longer than %d bytes", MaxKeyBytes)
	}
	if !utf8.ValidString(v) {
		return errors.New("the value is not UTF-8")
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("the value holds the control character %U", r)
		}
	}
	return nil
}

// String returns p as gNMI's path conventions write it, keys sorted by name
// and each value escaped, so ParsePath reads it back as p: "/" for the root.
func (p Path) String() string {
	var b strings.Builder
	if p.Origin != "" {
		b.WriteString(p.Origin)
		b.WriteByte(':')
	}
	if len(p.Elems) == 0 {
		b.WriteByte('/')
	}
	for _, e := range p.Elems {
		b.WriteByte('/')
		b.WriteString(e.Name)
		for _, name := range slices.Sorted(maps.Keys(e.Keys)) {
			b.WriteByte('[')
			b.WriteString(name)
			b.WriteByte('=')
			b.WriteString(strings.NewReplacer(`\`, `\\`, `]`, `\]`).Replace(e.Keys[name]))
			b.WriteByte(']')
		}
	}
	return b.String()
}

// Equal reports whether p and q name the same node: the same origin, and
// the same elements by name and keys.
func (p Path) Equal(q Path) bool {
	if p.Origin != q.Origin || len(p.Elems) != len(q.Elems) {
		return false
	}
	for i := range p.Elems {
		if p.Elems[i].Name != q.Elems[i].Name || !maps.Equal(p.Elems[i].Keys, q.Elems[i].Keys) {
			return false
		}
	}
	return true
}

// HasWildcard reports whether p holds a wildcard name or key value.
func (p Path) HasWildcard() bool {
	for _, e := range p.Elems {
		if e.Name == WildcardAny || e.Name == WildcardDeep {
			return true
		}
		for _, v := range e.Keys {
			if v == wildcardValue {
				return true
			}
		}
	}
	return false
}

// Join returns path under prefix: prefix's elements then path's, with
// path's origin, or prefix's when path has none.
func Join(prefix, path Path) Path {
	out := Path{Origin: path.Origin, Elems: append(slices.Clip(slices.Clone(prefix.Elems)), path.Elems...)}
	if out.Origin == "" {
		out.Origin = prefix.Origin
	}
	return out
}

// proto returns p as gNMI's wire Path. p must already be valid.
func (p Path) proto() *gpb.Path {
	out := &gpb.Path{Origin: p.Origin}
	for _, e := range p.Elems {
		elem := &gpb.PathElem{Name: e.Name}
		if len(e.Keys) > 0 {
			elem.Key = maps.Clone(e.Keys)
		}
		out.Elem = append(out.Elem, elem)
	}
	return out
}

// pathFromProto reads a path a device sent, held to the same rules as one
// this client sends (wildcards allowed, as a target may echo a subscription's
// own). A path in the element field gNMI 0.4 deprecated is refused rather
// than guessed at: its strings carry keys in a form no two targets agreed on.
func pathFromProto(pp *gpb.Path) (Path, error) {
	if pp == nil {
		return Path{}, nil
	}
	if len(pp.GetElement()) > 0 {
		return Path{}, errors.New("the device sent a path in the element field gNMI 0.4.0 deprecated, which this client does not read")
	}
	if len(pp.GetElem()) > MaxElems {
		return Path{}, fmt.Errorf("the device sent a path of %d elements, more than %d", len(pp.GetElem()), MaxElems)
	}
	p := Path{Origin: pp.GetOrigin()}
	for _, e := range pp.GetElem() {
		elem := Elem{Name: e.GetName()}
		if len(e.GetKey()) > 0 {
			if len(e.GetKey()) > MaxKeys {
				return Path{}, fmt.Errorf("the device sent an element with %d keys, more than %d", len(e.GetKey()), MaxKeys)
			}
			elem.Keys = maps.Clone(e.GetKey())
		}
		p.Elems = append(p.Elems, elem)
	}
	if err := p.Validate(true); err != nil {
		return Path{}, fmt.Errorf("the device sent a path this client does not accept: %w", err)
	}
	return p, nil
}
