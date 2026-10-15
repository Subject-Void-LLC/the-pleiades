// Package docedit: key paths, and setting or removing the value one
// names in a JSON document.
package docedit

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Segment is one step of a key path: a mapping's key, or, at a list, an
// index.
type Segment struct {
	Key string
	// Index, when IsIndex, is a list position; a segment written as a
	// whole number is an index at a list and a key at a mapping.
	Index   int
	IsIndex bool
}

// String is the segment as a path names it.
func (s Segment) String() string {
	if s.IsIndex {
		return strconv.Itoa(s.Index)
	}
	return s.Key
}

// maxSegments bounds a path.
const maxSegments = 64

// ParsePath reads a key path: a dotted string (server.port, items.0.name)
// or a list of segments ([server, "a.b", 0]) for a key holding a dot, an
// integer in a list being an index.
func ParsePath(raw any) ([]Segment, error) {
	var out []Segment
	switch t := raw.(type) {
	case string:
		if t == "" {
			return nil, errors.New("the key path is empty")
		}
		for _, part := range strings.Split(t, ".") {
			if part == "" {
				return nil, fmt.Errorf("the key path %q has an empty segment; give a list of segments for a key holding a dot", t)
			}
			out = append(out, segmentOf(part))
		}
	case []any:
		if len(t) == 0 {
			return nil, errors.New("the key path is empty")
		}
		for i, e := range t {
			switch s := e.(type) {
			case string:
				out = append(out, Segment{Key: s})
			case int:
				if s < 0 {
					return nil, fmt.Errorf("segment %d is a negative index", i)
				}
				out = append(out, Segment{Index: s, IsIndex: true})
			default:
				return nil, fmt.Errorf("segment %d is %T, not a key or an index", i, e)
			}
		}
	default:
		return nil, errors.New("the key path is a dotted string or a list of segments")
	}
	if len(out) > maxSegments {
		return nil, fmt.Errorf("the key path has over %d segments", maxSegments)
	}
	return out, nil
}

// segmentOf reads one dotted part: a whole number is an index at a list.
func segmentOf(part string) Segment {
	if n, err := strconv.Atoi(part); err == nil && n >= 0 && strconv.Itoa(n) == part {
		return Segment{Key: part, Index: n, IsIndex: true}
	}
	return Segment{Key: part}
}

// PathText is a path as a message names it.
func PathText(path []Segment) string {
	parts := make([]string, len(path))
	for i, s := range path {
		parts[i] = s.String()
	}
	return strings.Join(parts, ".")
}

// Get returns the value at path, false when there is none.
func (v *Value) Get(path []Segment) (*Value, bool) {
	cur := v
	for _, s := range path {
		next, ok := cur.child(s)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// child is the value one segment names under v.
func (v *Value) child(s Segment) (*Value, bool) {
	switch v.kind {
	case kindObject:
		for _, m := range v.members {
			if m.key == keyOf(s) {
				return m.val, true
			}
		}
	case kindArray:
		if s.IsIndex && s.Index < len(v.items) {
			return v.items[s.Index], true
		}
	}
	return nil, false
}

// Set puts val at path, creating a mapping for each missing key on the
// way; a key is added at the end of its mapping, and an index may name a
// list's next position to append. It refuses a path through a scalar, or
// an index past a list's end.
func (v *Value) Set(path []Segment, val *Value) error {
	if len(path) == 0 {
		return errors.New("the key path is empty")
	}
	cur := v
	for i, s := range path {
		last := i == len(path)-1
		switch cur.kind {
		case kindObject:
			key := keyOf(s)
			found := false
			for j := range cur.members {
				if cur.members[j].key == key {
					found = true
					if last {
						cur.members[j].val = val
						return nil
					}
					cur = cur.members[j].val
					break
				}
			}
			if !found {
				next := val
				if !last {
					next = &Value{kind: kindObject}
				}
				cur.members = append(cur.members, member{key: key, raw: quote(key), val: next})
				if last {
					return nil
				}
				cur = next
			}
		case kindArray:
			if !s.IsIndex {
				return fmt.Errorf("%s is a list, and %q is not an index", PathText(path[:i]), s.Key)
			}
			switch {
			case s.Index < len(cur.items):
				if last {
					cur.items[s.Index] = val
					return nil
				}
				cur = cur.items[s.Index]
			case s.Index == len(cur.items):
				next := val
				if !last {
					next = &Value{kind: kindObject}
				}
				cur.items = append(cur.items, next)
				if last {
					return nil
				}
				cur = next
			default:
				return fmt.Errorf("index %d is past the end of %s, which holds %d", s.Index, PathText(path[:i]), len(cur.items))
			}
		default:
			return fmt.Errorf("%s is a scalar, so %s cannot be set under it", orRoot(path[:i]), s)
		}
	}
	return nil
}

// Delete removes the value at path, reporting whether there was one.
func (v *Value) Delete(path []Segment) (bool, error) {
	if len(path) == 0 {
		return false, errors.New("the key path is empty")
	}
	parent, ok := v.Get(path[:len(path)-1])
	if !ok {
		return false, nil
	}
	s := path[len(path)-1]
	switch parent.kind {
	case kindObject:
		for j, m := range parent.members {
			if m.key == keyOf(s) {
				parent.members = append(parent.members[:j], parent.members[j+1:]...)
				return true, nil
			}
		}
	case kindArray:
		if s.IsIndex && s.Index < len(parent.items) {
			parent.items = append(parent.items[:s.Index], parent.items[s.Index+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// SameJSON reports whether two values are the same JSON: numbers by their
// text, members in order.
func SameJSON(a, b *Value) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytes.Equal(EncodeJSON(a, JSONFormat{}), EncodeJSON(b, JSONFormat{}))
}

// keyOf is a segment read as a mapping's key: an index given as a number
// is the key its digits spell.
func keyOf(s Segment) string {
	if s.Key == "" && s.IsIndex {
		return strconv.Itoa(s.Index)
	}
	return s.Key
}

// orRoot names a path, or the document for the empty one.
func orRoot(path []Segment) string {
	if len(path) == 0 {
		return "the document"
	}
	return PathText(path)
}
