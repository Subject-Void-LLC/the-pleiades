// Python semantics the ported parsers rely on: an insertion-ordered dict,
// a mutable list, Python's exceptions, truthiness, int(), str.strip(),
// str.split() and str.splitlines().
package iosparse

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pyErr is a Python exception raised where genieparser's own code raises
// one. The ported parsers panic with it and Parse recovers it into a
// *ParserError, so a malformed device output fails the parse the way it
// fails in Genie rather than producing a result Genie never would.
type pyErr struct {
	// kind is the Python exception class, such as KeyError or ValueError.
	kind string
	// msg describes what was missing or malformed.
	msg string
}

// portLimit is the pyErr kind for input the port refuses where
// genieparser would go on: a bound that keeps a parse of untrusted output
// from running unbounded. Genie raises nothing there.
const portLimit = "PortLimit"

// raise panics with a pyErr of the given Python exception class.
func raise(kind, format string, args ...any) {
	panic(&pyErr{kind: kind, msg: fmt.Sprintf(format, args...)})
}

// try runs fn as Python's "try: ... except Exception: pass" does: a pyErr
// raised inside it stops fn and is swallowed, and try reports whether one
// was. Any other panic is a defect in the port and is not swallowed.
func try(fn func()) (raised bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(*pyErr); !ok {
				panic(r)
			}
			raised = true
		}
	}()
	fn()
	return false
}

// dict is a Python dict: string keys kept in insertion order, which the
// parsers depend on wherever they iterate one or take its first key.
// Genie's int keys (a route's next-hop index, a CDP entry index) are held
// as their decimal strings, the key JSON gives them.
type dict struct {
	// keys holds each key once, in the order it was first set.
	keys []string
	// vals maps each key to its value.
	vals map[string]any
}

// newDict returns an empty dict.
func newDict() *dict {
	return &dict{vals: map[string]any{}}
}

// has reports whether k is a key, Python's "k in d".
func (d *dict) has(k string) bool {
	_, ok := d.vals[k]
	return ok
}

// get returns d.get(k): the value, or nil when k is absent.
func (d *dict) get(k string) any {
	return d.vals[k]
}

// set is d[k] = v.
func (d *dict) set(k string, v any) {
	if _, ok := d.vals[k]; !ok {
		d.keys = append(d.keys, k)
	}
	d.vals[k] = v
}

// at is d[k], raising KeyError when k is absent.
func (d *dict) at(k string) any {
	v, ok := d.vals[k]
	if !ok {
		raise("KeyError", "%q", k)
	}
	return v
}

// d is d[k] used as a dict: KeyError when absent, TypeError when the value
// is not a dict.
func (d *dict) d(k string) *dict {
	return asDict(d.at(k))
}

// l is d[k] used as a list: KeyError when absent, TypeError when the value
// is not a list.
func (d *dict) l(k string) *list {
	return asList(d.at(k))
}

// setdefault is d.setdefault(k, v): the existing value, or v once stored.
func (d *dict) setdefault(k string, v any) any {
	if cur, ok := d.vals[k]; ok {
		return cur
	}
	d.set(k, v)
	return v
}

// sub is d.setdefault(k, {}) used as a dict.
func (d *dict) sub(k string) *dict {
	return asDict(d.setdefault(k, newDict()))
}

// del is del d[k], raising KeyError when k is absent.
func (d *dict) del(k string) {
	if _, ok := d.vals[k]; !ok {
		raise("KeyError", "%q", k)
	}
	delete(d.vals, k)
	for i, key := range d.keys {
		if key == k {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
}

// update is d.update(o): every key of o, in o's order, set on d.
func (d *dict) update(o *dict) {
	for _, k := range o.keys {
		d.set(k, o.vals[k])
	}
}

// size is len(d).
func (d *dict) size() int {
	return len(d.keys)
}

// keyList is list(d.keys()), a copy safe to range over while d changes.
func (d *dict) keyList() []string {
	return append([]string(nil), d.keys...)
}

// asDict asserts v is a dict, raising TypeError when it is not.
func asDict(v any) *dict {
	d, ok := v.(*dict)
	if !ok {
		raise("TypeError", "%T is not a dict", v)
	}
	return d
}

// list is a Python list, shared by reference as Python shares one.
type list struct {
	// items holds the elements in order.
	items []any
}

// newList returns a list holding items.
func newList(items ...any) *list {
	return &list{items: append([]any{}, items...)}
}

// strList returns a list holding each string of items.
func strList(items []string) *list {
	l := &list{items: make([]any, 0, len(items))}
	for _, s := range items {
		l.items = append(l.items, s)
	}
	return l
}

// appendItem is l.append(v).
func (l *list) appendItem(v any) {
	l.items = append(l.items, v)
}

// concat is l + o, a new list.
func (l *list) concat(o *list) *list {
	out := &list{items: make([]any, 0, len(l.items)+len(o.items))}
	out.items = append(out.items, l.items...)
	out.items = append(out.items, o.items...)
	return out
}

// asList asserts v is a list, raising TypeError when it is not.
func asList(v any) *list {
	l, ok := v.(*list)
	if !ok {
		raise("TypeError", "%T is not a list", v)
	}
	return l
}

// toPlain converts a parsed value into plain Go: a dict becomes a
// map[string]any and a list a []any, recursively, so a caller never sees
// the port's internal types.
func toPlain(v any) any {
	switch t := v.(type) {
	case *dict:
		m := make(map[string]any, len(t.keys))
		for _, k := range t.keys {
			m[k] = toPlain(t.vals[k])
		}
		return m
	case *list:
		out := make([]any, 0, len(t.items))
		for _, item := range t.items {
			out = append(out, toPlain(item))
		}
		return out
	default:
		return v
	}
}

// truthy is Python's bool(v): false for None, False, 0, "" and an empty
// dict or list.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case int64:
		return t != 0
	case string:
		return t != ""
	case *dict:
		return t.size() > 0
	case *list:
		return len(t.items) > 0
	default:
		return true
	}
}

// lowerStr is Python's str.lower().
func lowerStr(s string) string {
	return strings.ToLower(s)
}

// contains is Python's "sub in s" for strings.
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// pyIsSpace reports whether r is whitespace to Python's str.isspace(),
// which counts the information separators U+001C to U+001F that Go's
// unicode.IsSpace does not.
func pyIsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// strip is Python's str.strip() with no argument.
func strip(s string) string {
	return strings.TrimFunc(s, pyIsSpace)
}

// rstrip is Python's str.rstrip() with no argument.
func rstrip(s string) string {
	return strings.TrimRightFunc(s, pyIsSpace)
}

// fields is Python's str.split() with no argument: runs of whitespace
// separate fields and none is empty.
func fields(s string) []string {
	return strings.FieldsFunc(s, pyIsSpace)
}

// splitlines is Python's str.splitlines(): it breaks on every line
// boundary Python recognizes, treats "\r\n" as one, and drops a trailing
// empty line.
func splitlines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			lines = append(lines, s[start:i])
			next := i + size
			// "\r\n" is one boundary, not two.
			if r == '\r' && next < len(s) && s[next] == '\n' {
				next++
			}
			start = next
			i = next
			continue
		}
		i += size
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// universalNewlines turns "\r\n" and a lone "\r" into "\n", which is what
// Python's text-mode file read does to a fixture and what Genie's device
// connection hands a parser.
func universalNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// pyInt is Python's int(s) for a decimal string: surrounding whitespace,
// a sign and single underscores between digits are accepted, anything else
// raises ValueError. A value beyond int64 also raises ValueError, where
// Python would return a larger integer: no IOS counter reaches it.
func pyInt(s string) int64 {
	v, ok := tryInt(s)
	if !ok {
		raise("ValueError", "invalid literal for int(): %q", s)
	}
	return v
}

// tryInt is pyInt reporting failure instead of raising, for the
// "try: int(x) except: ..." the parsers use.
func tryInt(s string) (int64, bool) {
	t := strip(s)
	neg := false
	if t != "" && (t[0] == '+' || t[0] == '-') {
		neg = t[0] == '-'
		t = t[1:]
	}
	if t == "" || t[0] == '_' || t[len(t)-1] == '_' || strings.Contains(t, "__") {
		return 0, false
	}
	var v uint64
	for _, r := range t {
		if r == '_' {
			continue
		}
		if r < '0' || r > '9' {
			return 0, false
		}
		// Refuse a value that would not fit before multiplying past it.
		if v > (math.MaxInt64-uint64(r-'0'))/10 {
			return 0, false
		}
		v = v*10 + uint64(r-'0')
	}
	if neg {
		return -int64(v), true
	}
	return int64(v), true
}

// capitalizeFirst is Python's s[0].capitalize() + s[1:]: the first
// character in title case and the rest unchanged.
func capitalizeFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return s
	}
	return string(unicode.ToTitle(r)) + s[size:]
}

// pySlice is Python's s[start:end] on a string of code points, clamped as
// Python clamps an index past either end. An end below zero means "to the
// end of the string".
func pySlice(runes []rune, start, end int) string {
	if end < 0 || end > len(runes) {
		end = len(runes)
	}
	if start > end {
		return ""
	}
	return string(runes[start:end])
}
