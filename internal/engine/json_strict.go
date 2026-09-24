// Package engine: the JSON half of the strict key check (schema_keys.go).
//
// encoding/json is more forgiving than a runbook can afford in two ways
// the key walker alone cannot see through: it matches a key to a field
// without regard to case, so {"FQCN": ...} fills Task.FQCN, and a repeated
// key silently keeps its last value. Both let a runbook say one thing and
// run another. So a JSON runbook is first read here, token by token, into
// a small tree that keeps every key exactly as written, refusing a
// repeated key anywhere in the document, and that tree is what the key
// walker checks.
package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// jsonKeyTree is one node of a JSON document as the key check sees it:
// an object's keys in order with their values, an array's items, or
// neither for a scalar.
type jsonKeyTree struct {
	isObject bool
	isArray  bool
	keys     []string
	values   []*jsonKeyTree
	items    []*jsonKeyTree
}

// identity implements keyTree. JSON has no aliases, so every node is its
// own identity.
func (t *jsonKeyTree) identity() any { return t }

// mapping implements keyTree. JSON carries no line numbers here, so each
// entry's line is zero.
func (t *jsonKeyTree) mapping() ([]keyEntry, bool) {
	if !t.isObject {
		return nil, false
	}
	entries := make([]keyEntry, len(t.keys))
	for i, k := range t.keys {
		entries[i] = keyEntry{key: k, value: t.values[i]}
	}
	return entries, true
}

// sequence implements keyTree.
func (t *jsonKeyTree) sequence() ([]keyTree, bool) {
	if !t.isArray {
		return nil, false
	}
	items := make([]keyTree, len(t.items))
	for i, item := range t.items {
		items[i] = item
	}
	return items, true
}

// maxJSONDepth bounds how deeply the tree reader nests. Its recursion is
// one Go call per level, so an unbounded payload of opening brackets
// would grow the stack with the input; a runbook nests at most
// maxTaskNestingDepth task levels, a handful of JSON levels each, so this
// ceiling is far above any real document.
const maxJSONDepth = 512

// errNotJSON marks a payload the tree reader could not parse at all. The
// caller lets the real decoder report such a payload in its own words.
var errNotJSON = errors.New("not a JSON document")

// parseJSONKeyTree reads payload into a jsonKeyTree, refusing any object
// that repeats a key and naming where. A payload that is not JSON returns
// an error wrapping errNotJSON.
func parseJSONKeyTree(payload []byte) (*jsonKeyTree, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	tree, err := readJSONValue(dec, "", 0)
	if err != nil {
		return nil, err
	}
	// A second value after the first is not one JSON document.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data after the top-level value", errNotJSON)
	}
	return tree, nil
}

// readJSONValue reads one value from dec. path names the value's place,
// for the duplicate-key error, and depth is how many containers enclose
// it.
func readJSONValue(dec *json.Decoder, path string, depth int) (*jsonKeyTree, error) {
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("JSON runbook nests deeper than %d levels at %s", maxJSONDepth, path)
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNotJSON, err)
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return &jsonKeyTree{}, nil
	}
	switch delim {
	case '{':
		return readJSONObject(dec, path, depth)
	case '[':
		return readJSONArray(dec, path, depth)
	default:
		return nil, fmt.Errorf("%w: unexpected %q", errNotJSON, delim)
	}
}

// readJSONObject reads an object's members up to its closing brace.
func readJSONObject(dec *json.Decoder, path string, depth int) (*jsonKeyTree, error) {
	node := &jsonKeyTree{isObject: true}
	seen := make(map[string]bool)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errNotJSON, err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("%w: an object key must be a string", errNotJSON)
		}
		if seen[key] {
			where := "at the top level"
			if path != "" {
				where = "under " + path
			}
			return nil, fmt.Errorf("duplicate key %q %s: a JSON decoder keeps only the last one, so the runbook would not run what it says", key, where)
		}
		seen[key] = true
		value, err := readJSONValue(dec, joinKeyPath(path, key), depth+1)
		if err != nil {
			return nil, err
		}
		node.keys = append(node.keys, key)
		node.values = append(node.values, value)
	}
	// Consume the closing brace.
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("%w: %v", errNotJSON, err)
	}
	return node, nil
}

// readJSONArray reads an array's items up to its closing bracket.
func readJSONArray(dec *json.Decoder, path string, depth int) (*jsonKeyTree, error) {
	node := &jsonKeyTree{isArray: true}
	for i := 0; dec.More(); i++ {
		item, err := readJSONValue(dec, fmt.Sprintf("%s[%d]", path, i), depth+1)
		if err != nil {
			return nil, err
		}
		node.items = append(node.items, item)
	}
	// Consume the closing bracket.
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("%w: %v", errNotJSON, err)
	}
	return node, nil
}
