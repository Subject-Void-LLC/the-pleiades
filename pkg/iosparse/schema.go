// An independent reimplementation of the schema check MetaParser.parse, in
// Cisco's separate genie library, runs on every parser's result. It is
// written from that check's documented behavior and the fixtures; none of
// its code was copied. The schemas themselves are genieparser's
// (schemas.go).
package iosparse

import (
	"fmt"
	"strings"
)

// kind is a Python type a schema names for a leaf value.
type kind int

// The leaf types genieparser's schemas for the ported parsers name.
const (
	// kStr is Python's str.
	kStr kind = iota
	// kInt is Python's int, which a bool also satisfies, since Python's
	// bool is a subclass of int.
	kInt
	// kBool is Python's bool.
	kBool
	// kList is Python's list.
	kList
	// kNone is None, which only appears inside Or.
	kNone
)

// orKinds is the schema engine's Or(...) of leaf types.
type orKinds []kind

// field is one key of a schema dict.
type field struct {
	// key is the literal key; empty for Any().
	key string
	// wild marks Any(), which matches any key not named literally.
	wild bool
	// optional marks Optional(...): a literal key that may be absent.
	optional bool
	// val is the schema for the key's value: a kind, an orKinds or a
	// schema.
	val any
}

// schema is a schema dict.
type schema []field

// req is a required literal key.
func req(key string, val any) field { return field{key: key, val: val} }

// opt is Optional(key).
func opt(key string, val any) field { return field{key: key, optional: true, val: val} }

// anyKey is Any() (or Optional(Any())) as a key.
func anyKey(val any) field { return field{wild: true, val: val} }

// SchemaError reports a parser result that does not fit genieparser's
// schema for it, which Genie's MetaParser.parse refuses with a
// SchemaMissingKeyError or SchemaTypeError rather than returning.
type SchemaError struct {
	// Parser is the genieparser class whose schema refused the result.
	Parser string
	// Path is the key path to the value, joined with "/".
	Path string
	// Problem says what is wrong there.
	Problem string
}

// Error implements error.
func (e *SchemaError) Error() string {
	return fmt.Sprintf("%s: result does not fit genie's schema at %s: %s", e.Parser, e.Path, e.Problem)
}

// check validates data against s. It enforces what the schema engine
// certainly enforces: every required literal key is present, and every
// value has the type its schema names. Two things are deliberately
// lenient, because the engine's own source is compiled and its rule
// could not be read: a required Any() key accepts an empty dict, and a key
// the schema does not name is kept rather than refused.
func check(s any, data any, path []string) error {
	switch t := s.(type) {
	case kind:
		if !kindFits(t, data) {
			return &SchemaError{Path: joinPath(path), Problem: fmt.Sprintf("%s is not %s", pyTypeName(data), kindName(t))}
		}
	case orKinds:
		for _, k := range t {
			if kindFits(k, data) {
				return nil
			}
		}
		return &SchemaError{Path: joinPath(path), Problem: fmt.Sprintf("%s fits none of its allowed types", pyTypeName(data))}
	case schema:
		d, ok := data.(*dict)
		if !ok {
			return &SchemaError{Path: joinPath(path), Problem: fmt.Sprintf("%s is not a dict", pyTypeName(data))}
		}
		return checkDict(t, d, path)
	}
	return nil
}

// checkDict validates a dict against a schema dict.
func checkDict(s schema, d *dict, path []string) error {
	var wild *field
	named := map[string]field{}
	for i := range s {
		f := s[i]
		if f.wild {
			wild = &s[i]
			continue
		}
		named[f.key] = f
		if !f.optional && !d.has(f.key) {
			return &SchemaError{Path: joinPath(append(path, f.key)), Problem: "required key missing"}
		}
	}
	for _, k := range d.keys {
		f, ok := named[k]
		if !ok {
			if wild == nil {
				continue
			}
			f = *wild
		}
		if err := check(f.val, d.vals[k], append(append([]string(nil), path...), k)); err != nil {
			return err
		}
	}
	return nil
}

// kindFits reports whether v is of Python type k.
func kindFits(k kind, v any) bool {
	switch k {
	case kStr:
		_, ok := v.(string)
		return ok
	case kInt:
		switch v.(type) {
		case int64, bool:
			return true
		}
		return false
	case kBool:
		_, ok := v.(bool)
		return ok
	case kList:
		_, ok := v.(*list)
		return ok
	case kNone:
		return v == nil
	}
	return false
}

// kindName is a kind's Python name.
func kindName(k kind) string {
	return [...]string{"str", "int", "bool", "list", "None"}[k]
}

// pyTypeName is the Python type name of a parsed value.
func pyTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "None"
	case string:
		return "str"
	case int64:
		return "int"
	case bool:
		return "bool"
	case *dict:
		return "dict"
	case *list:
		return "list"
	}
	return fmt.Sprintf("%T", v)
}

// joinPath renders a key path for an error, "(top)" for the root.
func joinPath(path []string) string {
	if len(path) == 0 {
		return "(top)"
	}
	return strings.Join(path, "/")
}
