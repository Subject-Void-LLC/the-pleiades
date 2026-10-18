// Package docedit edits one value inside a JSON or YAML document and
// leaves the rest of it as it was: JSON through an ordered parser that
// keeps every key's place and every number and string exactly as written,
// YAML through go.yaml.in/yaml/v3's nodes, which keep comments and order.
// It refuses what two readers could read differently (a duplicate key) and
// what one edit could not change alone (a YAML alias).
package docedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Bounds on a document.
const (
	MaxBytes = 4 << 20
	maxDepth = 64
)

// kind is a JSON value's kind.
type kind int

const (
	kindNull kind = iota
	kindBool
	kindNumber
	kindString
	kindArray
	kindObject
)

// Value is a JSON value that keeps its members in order and its scalars
// as written.
type Value struct {
	kind    kind
	raw     string // a scalar's text as written (a string's with its quotes)
	members []member
	items   []*Value
}

// member is one key of an object.
type member struct {
	key string
	raw string // the key as written, quotes included
	val *Value
}

// JSONFormat is how a document was laid out, so it is written back the
// same way: its indent unit (empty for one line), and whether it ended in
// a line break.
type JSONFormat struct {
	Indent   string
	Trailing bool
}

// ParseJSON reads a JSON document, refusing invalid UTF-8, a duplicate key
// at any depth, text after the document, nesting past 64, and a document
// over MaxBytes.
func ParseJSON(data []byte) (*Value, JSONFormat, error) {
	if len(data) > MaxBytes {
		return nil, JSONFormat{}, fmt.Errorf("the document is over %d bytes", MaxBytes)
	}
	if !utf8.Valid(data) {
		return nil, JSONFormat{}, errors.New("the document is not UTF-8")
	}
	p := &jsonParser{src: data}
	p.space()
	v, err := p.value(0)
	if err != nil {
		return nil, JSONFormat{}, err
	}
	p.space()
	if p.pos != len(p.src) {
		return nil, JSONFormat{}, p.errorf("text after the document")
	}
	return v, detectFormat(data), nil
}

// detectFormat reads the indent unit from the first indented line.
func detectFormat(data []byte) JSONFormat {
	f := JSONFormat{Trailing: bytes.HasSuffix(data, []byte("\n"))}
	body := bytes.TrimRight(data, " \t\r\n")
	nl := bytes.IndexByte(body, '\n')
	if nl < 0 {
		return f
	}
	line := body[nl+1:]
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	f.Indent = string(line[:n])
	if f.Indent == "" {
		f.Indent = "  "
	}
	return f
}

// EncodeJSON writes v in format f: one line when f has no indent, else
// one member or item per line at f's indent unit.
func EncodeJSON(v *Value, f JSONFormat) []byte {
	var b bytes.Buffer
	writeJSON(&b, v, f.Indent, 0)
	if f.Trailing {
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func writeJSON(b *bytes.Buffer, v *Value, indent string, depth int) {
	nl := func(d int) {
		if indent != "" {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat(indent, d))
		}
	}
	switch v.kind {
	case kindObject:
		if len(v.members) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, m := range v.members {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			b.WriteString(m.raw)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			writeJSON(b, m.val, indent, depth+1)
		}
		nl(depth)
		b.WriteByte('}')
	case kindArray:
		if len(v.items) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, it := range v.items {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			writeJSON(b, it, indent, depth+1)
		}
		nl(depth)
		b.WriteByte(']')
	default:
		b.WriteString(v.raw)
	}
}

// FromAny builds a Value from a decoded YAML or JSON value: a mapping's
// keys sorted, since a decoded mapping has no order of its own.
func FromAny(x any) (*Value, error) {
	switch t := x.(type) {
	case nil:
		return &Value{kind: kindNull, raw: "null"}, nil
	case bool:
		return &Value{kind: kindBool, raw: strconv.FormatBool(t)}, nil
	case int:
		return &Value{kind: kindNumber, raw: strconv.Itoa(t)}, nil
	case int64:
		return &Value{kind: kindNumber, raw: strconv.FormatInt(t, 10)}, nil
	case uint64:
		return &Value{kind: kindNumber, raw: strconv.FormatUint(t, 10)}, nil
	case float64:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		return &Value{kind: kindNumber, raw: string(b)}, nil
	case string:
		return &Value{kind: kindString, raw: quote(t)}, nil
	case []any:
		v := &Value{kind: kindArray}
		for _, e := range t {
			ev, err := FromAny(e)
			if err != nil {
				return nil, err
			}
			v.items = append(v.items, ev)
		}
		return v, nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		v := &Value{kind: kindObject}
		for _, k := range keys {
			ev, err := FromAny(t[k])
			if err != nil {
				return nil, err
			}
			v.members = append(v.members, member{key: k, raw: quote(k), val: ev})
		}
		return v, nil
	}
	return nil, fmt.Errorf("a value of type %T cannot be written as JSON", x)
}

// quote writes s as a JSON string, without HTML escaping.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

// JSONText is v written on one line, for a diff.
func JSONText(v *Value) string {
	if v == nil {
		return ""
	}
	return string(EncodeJSON(v, JSONFormat{}))
}
