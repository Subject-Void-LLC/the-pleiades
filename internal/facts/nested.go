// Package facts: the secret-name rule applied inside a value, not only to its
// key.
package facts

import (
	"maps"
	"slices"
)

// secretField returns the name of the first field, at any depth of value's
// objects and lists, that secretName says names a secret, and whether there
// is one. value is a prepared fact's normalized JSON (maps, lists, strings,
// numbers, booleans and nil), so a map is always map[string]any.
//
// Prepare checked only a fact's own key, so an object fact holding
// {"db": {"password": ...}} was stored whole (FAILURE_PATTERNS 920). Only the
// name decides; a field's value is never read here, and Prepare's other
// checks still read every value as text. Unlike a family key's item name
// (nameOf), a map key inside a value is always read as a field name: a
// value keyed by item names, one of them named like a secret, is refused
// whole. That errs toward not storing; a built-in keyed by item names
// (net.ios.facts's interfaces) has none the rule matches.
func secretField(value any, secretName func(string) bool) (string, bool) {
	switch v := value.(type) {
	case map[string]any:
		// In order, so the field a refusal names is the same on every run.
		for _, name := range slices.Sorted(maps.Keys(v)) {
			if secretName(name) {
				return name, true
			}
			if found, ok := secretField(v[name], secretName); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range v {
			if found, ok := secretField(child, secretName); ok {
				return found, true
			}
		}
	}
	return "", false
}
