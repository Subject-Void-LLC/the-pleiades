// Masking a value as data rather than as text: the maps, lists and scalars
// a Collection method reports as its stats, before anything prints them.
package redact

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Value masks v against the shared ruleset, with secrets masked alongside
// the process-wide literal set, exactly as Text does for free text. See
// Masker.Value for what it does to each shape.
func Value(secrets []string, v any) any {
	return Shared().Value(secrets, v)
}

// Value masks v, a value a method reported, as data rather than as text.
//
// A map entry whose key a key rule names (password, token, private_key and
// the rest) is replaced by Marker whatever it holds: the name says the
// value is a secret, which free text can never say, so this catches a
// secret whose bytes nothing registered. Every other string, map key
// included, is masked as Text masks it. A number or a bool whose written
// form Text would change is replaced by that masked text.
//
// Maps and lists are walked, and the common shapes keep their type
// (map[string]any, []any, []string, map[string]string, []map[string]any),
// so text rendered from the result reads as it would have from v. Any
// other shape, a struct above all, is first converted to the maps, lists
// and scalars JSON writes it as, so none of its fields escapes the walk.
// v itself is never changed.
func (m *Masker) Value(extra []string, v any) any {
	return m.value(extra, v, 0)
}

// maxDepth bounds the walk. Stats are shallow; a deeper value is a cycle
// or something that is not stats, and is masked as text instead.
const maxDepth = 64

// value is Value at a depth.
func (m *Masker) value(extra []string, v any, depth int) any {
	if depth > maxDepth {
		return m.Text(extra, fmt.Sprint(v))
	}
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return m.Text(extra, t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[m.Text(extra, k)] = m.entry(extra, k, item, depth)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(t))
		for k, item := range t {
			if m.secretKey(k) {
				out[m.Text(extra, k)] = Marker
				continue
			}
			out[m.Text(extra, k)] = m.Text(extra, item)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = m.value(extra, item, depth+1)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, item := range t {
			out[i] = m.Text(extra, item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(t))
		for i, item := range t {
			out[i], _ = m.value(extra, item, depth+1).(map[string]any)
		}
		return out
	case time.Time:
		// Written as itself by both JSON and text; a time holds no secret.
		return t
	}
	if scalar(v) {
		// A number or a bool: kept as it is unless its written form holds
		// something Text masks.
		if written := fmt.Sprint(v); m.Text(extra, written) != written {
			return m.Text(extra, written)
		}
		return v
	}
	return m.value(extra, generic(v), depth+1)
}

// entry masks one map entry's value: all of it when its key is a secret's
// name, else walked as any other value.
func (m *Masker) entry(extra []string, key string, v any, depth int) any {
	if m.secretKey(key) {
		return Marker
	}
	return m.value(extra, v, depth+1)
}

// secretKey reports whether a key rule names key, ignoring case.
func (m *Masker) secretKey(key string) bool {
	_, secret := m.keys[strings.ToLower(key)]
	return secret
}

// scalar reports whether v is a bool or a number of any size.
func scalar(v any) bool {
	switch reflect.ValueOf(v).Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// generic converts v to the maps, lists and scalars JSON would write it
// as. A value JSON cannot write (a channel, a function) becomes its text.
func generic(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Sprint(v)
	}
	return out
}
