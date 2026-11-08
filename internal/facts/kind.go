// Package facts: what a method declares about each fact it emits (Phase 41b),
// as the engine hands it to Prepare: whether the fact is stable or volatile,
// and the JSON type its value takes.
package facts

import (
	"encoding/json"
	"fmt"
)

// Kind says how a fact's values are kept.
type Kind string

const (
	// KindStable is a fact whose change means the device changed (a kernel
	// version, a package's version). Every value it has had is kept, in
	// order, and a change is drift.
	KindStable Kind = "stable"
	// KindVolatile is a fact that moves on its own (uptime, free memory).
	// Only its latest value is kept, and a change is never drift.
	KindVolatile Kind = "volatile"
)

// Valid reports whether k is one of the two kinds.
func (k Kind) Valid() bool { return k == KindStable || k == KindVolatile }

// ValueType is the JSON type a fact's value takes.
type ValueType string

const (
	// TypeString is a JSON string.
	TypeString ValueType = "string"
	// TypeInteger is a JSON number with no fraction or exponent.
	TypeInteger ValueType = "integer"
	// TypeNumber is any JSON number.
	TypeNumber ValueType = "number"
	// TypeBoolean is true or false.
	TypeBoolean ValueType = "boolean"
	// TypeList is a JSON array.
	TypeList ValueType = "list"
	// TypeObject is a JSON object.
	TypeObject ValueType = "object"
)

// Spec is what a method declared about one fact: its kind and value type,
// and for a key in a declared family whose names after the prefix are items'
// (collection.FactSpec.ItemNames), that prefix.
type Spec struct {
	Kind Kind
	Type ValueType
	// Family is the declared prefix of an item family the key belongs to
	// (package.), empty for a key declared on its own or in a family whose
	// names are fields.
	Family string
}

// checkType reports why normalized, a value as Canonical decoded it (numbers
// as json.Number), is not of type t, or "" when it is. A JSON null is no
// type's value: a method with nothing to report leaves the fact out.
func checkType(t ValueType, normalized any) string {
	got := jsonType(normalized)
	switch {
	case got == t:
		return ""
	case t == TypeNumber && got == TypeInteger:
		return ""
	}
	return fmt.Sprintf("its value is %s, and the method declares it %s", describe(got), t)
}

// jsonType names a decoded value's JSON type, "" for null or anything else.
func jsonType(v any) ValueType {
	switch x := v.(type) {
	case string:
		return TypeString
	case bool:
		return TypeBoolean
	case json.Number:
		if _, err := x.Int64(); err == nil {
			return TypeInteger
		}
		// A whole number too large for int64 is still an integer.
		for _, r := range x.String() {
			if r == '.' || r == 'e' || r == 'E' {
				return TypeNumber
			}
		}
		return TypeInteger
	case []any:
		return TypeList
	case map[string]any:
		return TypeObject
	}
	return ""
}

// describe names a value type in a message, null for the empty one.
func describe(t ValueType) string {
	if t == "" {
		return "null"
	}
	return "a " + string(t)
}

// SpecOf is a declaration (collection.FactSpec's kind, type, key and
// whether the key is the prefix of a family of items, NamesItems) as Prepare
// reads it. Callers pass the fields, so the rules here stay apart from a
// manifest's own types; the package reaches pkg/collection only for the
// static first-party groups (Provenance.CheckCollection).
func SpecOf(kind, valueType, key string, items bool) Spec {
	spec := Spec{Kind: Kind(kind), Type: ValueType(valueType)}
	if items {
		spec.Family = key
	}
	return spec
}
