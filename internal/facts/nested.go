// Package facts: the secret-name rule applied inside a value, not only to its
// key.
package facts

// secretField returns the name of the first field, at any depth of value's
// objects and lists, that secretName says names a secret, and whether there
// is one. value is a prepared fact's normalized JSON (maps, lists, strings,
// numbers, booleans and nil), so a map is always map[string]any.
//
// Prepare checked only a fact's own key, so an object fact holding
// {"db": {"password": ...}} was stored whole (FAILURE_PATTERNS 920). Only the
// name decides; a field's value is never read here, and Prepare's other
// checks still read every value as text.
func secretField(value any, secretName func(string) bool) (string, bool) {
	switch v := value.(type) {
	case map[string]any:
		for name, child := range v {
			if secretName(name) {
				return name, true
			}
			if found, ok := secretField(child, secretName); ok {
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
