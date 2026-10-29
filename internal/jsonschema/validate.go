// The check of a decoded JSON document against a schema Builder wrote.
package jsonschema

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Validator checks a decoded JSON value against a generated schema, for the
// subset of JSON Schema Builder writes. It is strict about the producer: an
// object may carry no property its schema does not declare, and a string
// with published known values must be one of them, though a reader of an
// open contract is held to neither. It also records which declared
// properties the value filled, so a test can prove a fixture reaches the
// whole schema rather than the part it happens to touch.
type Validator struct {
	// Defs are the schema's $defs.
	Defs map[string]any
	// RootName names the root object in Declared, "Report" when empty.
	RootName string
	// Declared and Seen are every "Type.property" the schema declares and
	// the ones the value filled; Nullable and SeenNull are the ones whose
	// schema admits null (a slice's aside, which one value cannot both fill
	// and leave empty) and the ones the value left null.
	Declared []string
	Seen     map[string]bool
	Nullable map[string]bool
	SeenNull map[string]bool
}

// NewValidator returns a Validator for schema, a schema document as a
// consumer decodes it from its JSON.
func NewValidator(schema map[string]any) *Validator {
	defs, _ := schema["$defs"].(map[string]any)
	return &Validator{Defs: defs, Seen: map[string]bool{}, Nullable: map[string]bool{}, SeenNull: map[string]bool{}}
}

// Unfilled returns every declared property no checked value gave a value.
func (s *Validator) Unfilled() []string {
	var out []string
	for _, declared := range s.Declared {
		if !s.Seen[declared] {
			out = append(out, declared)
		}
	}
	return out
}

// Check returns every way v breaks schema, at path.
func (s *Validator) Check(path string, v any, schema map[string]any) []string {
	if ref, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		def, _ := s.Defs[name].(map[string]any)
		return s.object(path, name, v, def)
	}
	if options, ok := schema["oneOf"].([]any); ok {
		for _, o := range options {
			if len(s.Check(path, v, o.(map[string]any))) == 0 {
				return nil
			}
		}
		return []string{fmt.Sprintf("%s matches none of its schema's alternatives", path)}
	}
	if enum, ok := schema["enum"].([]any); ok {
		if !slices.Contains(enum, v) {
			return []string{fmt.Sprintf("%s = %v is not one of %v", path, v, enum)}
		}
		return nil
	}
	// A schema with no type admits any value: an any field's.
	if schema["type"] != nil && !typeMatches(v, schema["type"]) {
		return []string{fmt.Sprintf("%s = %v is not %v", path, v, schema["type"])}
	}
	// A producer is held to the values it publishes as known, though a
	// reader is not: what this producer writes, it has to have declared.
	if known := asList(schema["examples"]); known != nil {
		if str, ok := v.(string); ok && !slices.Contains(known, any(str)) {
			return []string{fmt.Sprintf("%s = %q is not one of the values the schema publishes, %v", path, str, known)}
		}
	}
	if schema["properties"] != nil {
		root := s.RootName
		if root == "" {
			root = "Report"
		}
		return s.object(path, root, v, schema)
	}
	var problems []string
	if values, ok := schema["additionalProperties"].(map[string]any); ok {
		obj, _ := v.(map[string]any)
		for _, key := range sortedKeys(obj) {
			problems = append(problems, s.Check(path+"."+key, obj[key], values)...)
		}
	}
	// A list under a schema with no items (an any field's) may hold anything.
	if itemSchema, ok := schema["items"].(map[string]any); ok {
		items, _ := v.([]any)
		for i, item := range items {
			problems = append(problems, s.Check(fmt.Sprintf("%s[%d]", path, i), item, itemSchema)...)
		}
	}
	return problems
}

// object checks an object against its definition, typeName naming it.
func (s *Validator) object(path, typeName string, v any, def map[string]any) []string {
	obj, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s is not an object", path)}
	}
	props, _ := def["properties"].(map[string]any)
	var problems []string
	for _, name := range sortedKeys(props) {
		key := typeName + "." + name
		if !slices.Contains(s.Declared, key) {
			s.Declared = append(s.Declared, key)
		}
		value, present := obj[name]
		if present && value != nil {
			s.Seen[key] = true
		}
		if admitsNull(props[name].(map[string]any)) {
			s.Nullable[key] = true
			if present && value == nil {
				s.SeenNull[key] = true
			}
		}
		if !present {
			if required, _ := def["required"].([]any); slices.Contains(required, any(name)) {
				problems = append(problems, fmt.Sprintf("%s lacks required %s", path, name))
			}
			continue
		}
		problems = append(problems, s.Check(path+"."+name, value, props[name].(map[string]any))...)
	}
	for _, name := range sortedKeys(obj) {
		if _, ok := props[name]; !ok {
			problems = append(problems, fmt.Sprintf("%s has %s, which the schema does not declare", path, name))
		}
	}
	return problems
}

// admitsNull reports whether a property's schema offers null as its own
// alternative: a pointer's oneOf, or an enum listing null.
func admitsNull(schema map[string]any) bool {
	for _, o := range asList(schema["oneOf"]) {
		if o.(map[string]any)["type"] == "null" {
			return true
		}
	}
	return slices.Contains(asList(schema["enum"]), nil)
}

// asList returns v as a list, or nil.
func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

// sortedKeys returns m's keys in order, so problems print stably.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// typeMatches reports whether v is of JSON type want: one name, or a list.
func typeMatches(v any, want any) bool {
	names, _ := want.([]any)
	if name, ok := want.(string); ok {
		names = []any{name}
	}
	for _, name := range names {
		switch name {
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "integer":
			if f, ok := v.(float64); ok && f == float64(int64(f)) {
				return true
			}
		case "number":
			if _, ok := v.(float64); ok {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "array":
			if _, ok := v.([]any); ok {
				return true
			}
		case "object":
			if _, ok := v.(map[string]any); ok {
				return true
			}
		case "null":
			if v == nil {
				return true
			}
		}
	}
	return false
}
