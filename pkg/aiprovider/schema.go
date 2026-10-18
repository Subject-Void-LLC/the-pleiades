// Package aiprovider: holding a structured answer to its JSON schema.
package aiprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// schemaKeywords are the JSON Schema keywords this checks; a schema using
// any other is refused rather than half-checked, since an answer called
// valid against a keyword never checked is a validation that lied.
var schemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true,
	"enum": true, "const": true, "minimum": true, "maximum": true, "minLength": true, "maxLength": true,
	"minItems": true, "maxItems": true,
	// Annotations, which constrain nothing.
	"title": true, "description": true, "$schema": true, "examples": true, "default": true,
}

// Schema is a parsed JSON schema in the subset this checks.
type Schema struct {
	raw map[string]any
}

// ParseSchema reads a schema, refusing one with a keyword outside the
// subset (type, properties, required, additionalProperties, items, enum,
// const, minimum, maximum, minLength, maxLength, minItems, maxItems, and
// annotations), at any depth.
func ParseSchema(data []byte) (*Schema, error) {
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("the schema is not a JSON object: %w", err)
	}
	if err := checkKeywords(raw, "#"); err != nil {
		return nil, err
	}
	return &Schema{raw: raw}, nil
}

// checkKeywords refuses a keyword outside the subset, naming where.
func checkKeywords(s map[string]any, at string) error {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !schemaKeywords[k] {
			return fmt.Errorf("the schema uses %q at %s, which this does not check; keep to type, properties, required, additionalProperties, items, enum, const, minimum, maximum, minLength, maxLength, minItems and maxItems", k, at)
		}
	}
	if err := checkShapes(s, at); err != nil {
		return err
	}
	if props, ok := s["properties"].(map[string]any); ok {
		for name, sub := range props {
			m, ok := sub.(map[string]any)
			if !ok {
				return fmt.Errorf("the schema's property %s at %s is not a schema", name, at)
			}
			if err := checkKeywords(m, at+"/properties/"+name); err != nil {
				return err
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		if err := checkKeywords(items, at+"/items"); err != nil {
			return err
		}
	}
	if ap, ok := s["additionalProperties"].(map[string]any); ok {
		if err := checkKeywords(ap, at+"/additionalProperties"); err != nil {
			return err
		}
	}
	return nil
}

// checkShapes refuses a keyword whose value is not the JSON type the
// keyword takes: validation reads such a value as absent, so a schema
// holding one would parse, be sent to the provider and check nothing.
func checkShapes(s map[string]any, at string) error {
	bad := func(k, want string) error {
		return fmt.Errorf("the schema's %q at %s must be %s", k, at, want)
	}
	for k, v := range s {
		switch k {
		case "type":
			switch t := v.(type) {
			case string:
			case []any:
				for _, one := range t {
					if _, ok := one.(string); !ok {
						return bad(k, "a type name or a list of them")
					}
				}
			default:
				return bad(k, "a type name or a list of them")
			}
		case "properties":
			if _, ok := v.(map[string]any); !ok {
				return bad(k, "an object of schemas")
			}
		case "required":
			list, ok := v.([]any)
			if !ok {
				return bad(k, "a list of names")
			}
			for _, one := range list {
				if _, ok := one.(string); !ok {
					return bad(k, "a list of names")
				}
			}
		case "additionalProperties":
			switch v.(type) {
			case bool, map[string]any:
			default:
				return bad(k, "true, false or a schema")
			}
		case "items":
			if _, ok := v.(map[string]any); !ok {
				return bad(k, "a schema")
			}
		case "enum":
			if _, ok := v.([]any); !ok {
				return bad(k, "a list")
			}
		case "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems":
			if _, ok := number(v); !ok {
				return bad(k, "a number")
			}
		}
	}
	return nil
}

// Validate holds an answer to the schema, naming the first violation.
func (s *Schema) Validate(answer []byte) error {
	dec := json.NewDecoder(bytes.NewReader(answer))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("the answer is not JSON: %w", err)
	}
	if dec.More() {
		return errors.New("the answer holds more than one JSON value")
	}
	return validate(s.raw, v, "$")
}

// shown is a value from the answer or the schema, cut and made printable
// so an error never carries the answer whole.
func shown(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "(unprintable)"
	}
	return remoteexec.Excerpt(string(b), 128)
}

// where is a path into the answer, cut and made printable.
func where(path string) string { return remoteexec.Excerpt(path, 256) }

// key is a name from the answer, cut and made printable for a path.
func key(k string) string { return remoteexec.Excerpt(k, 64) }

// validate holds v to schema s at path.
func validate(s map[string]any, v any, path string) error {
	if t, ok := s["type"]; ok && !typeMatches(t, v) {
		return fmt.Errorf("%s is %s, not %s", where(path), typeOf(v), remoteexec.Excerpt(fmt.Sprint(t), 64))
	}
	if enum, ok := s["enum"].([]any); ok && !contains(enum, v) {
		return fmt.Errorf("%s is %s, not one of %s", where(path), shown(v), shown(enum))
	}
	if c, ok := s["const"]; ok && !equal(c, v) {
		return fmt.Errorf("%s is %s, not %s", where(path), shown(v), shown(c))
	}
	switch v := v.(type) {
	case json.Number:
		f, _ := v.Float64()
		if lim, ok := number(s["minimum"]); ok && f < lim {
			return fmt.Errorf("%s is %s, below %v", where(path), shown(v), lim)
		}
		if lim, ok := number(s["maximum"]); ok && f > lim {
			return fmt.Errorf("%s is %s, above %v", where(path), shown(v), lim)
		}
	case string:
		n := float64(utf8.RuneCountInString(v))
		if lim, ok := number(s["minLength"]); ok && n < lim {
			return fmt.Errorf("%s is shorter than %v", where(path), lim)
		}
		if lim, ok := number(s["maxLength"]); ok && n > lim {
			return fmt.Errorf("%s is longer than %v", where(path), lim)
		}
	case []any:
		if lim, ok := number(s["minItems"]); ok && float64(len(v)) < lim {
			return fmt.Errorf("%s has fewer than %v items", where(path), lim)
		}
		if lim, ok := number(s["maxItems"]); ok && float64(len(v)) > lim {
			return fmt.Errorf("%s has more than %v items", where(path), lim)
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, item := range v {
				if err := validate(items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if name, _ := r.(string); name != "" {
					if _, present := v[name]; !present {
						return fmt.Errorf("%s has no %s", where(path), name)
					}
				}
			}
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sub, ok := props[k].(map[string]any); ok {
				if err := validate(sub, v[k], path+"."+key(k)); err != nil {
					return err
				}
				continue
			}
			switch ap := s["additionalProperties"].(type) {
			case bool:
				if !ap {
					return fmt.Errorf("%s has %s, which the schema does not allow", where(path), key(k))
				}
			case map[string]any:
				if err := validate(ap, v[k], path+"."+key(k)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// typeMatches reports whether v is of type t (a name or a list of names).
func typeMatches(t, v any) bool {
	switch t := t.(type) {
	case string:
		return isType(t, v)
	case []any:
		for _, one := range t {
			if name, _ := one.(string); isType(name, v) {
				return true
			}
		}
	}
	return false
}

// isType reports whether v is the JSON type name.
func isType(name string, v any) bool {
	switch name {
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		return err == nil && f == math.Trunc(f)
	case "number":
		_, ok := v.(json.Number)
		return ok
	}
	return typeOf(v) == name
}

// typeOf is v's JSON type name.
func typeOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// number reads a schema's numeric limit.
func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

// contains reports whether list holds a value equal to v.
func contains(list []any, v any) bool {
	for _, x := range list {
		if equal(x, v) {
			return true
		}
	}
	return false
}

// equal compares two JSON values as JSON, numbers by value (so 1 and
// 1.0 are equal).
func equal(a, b any) bool {
	if na, ok := a.(json.Number); ok {
		if nb, ok := b.(json.Number); ok {
			fa, err1 := na.Float64()
			fb, err2 := nb.Float64()
			return err1 == nil && err2 == nil && fa == fb
		}
	}
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}
