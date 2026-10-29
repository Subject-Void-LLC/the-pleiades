// Package redact: the filter for variables a device brings.
package redact

// CleanVars returns a copy of vars with every entry dropped whose key names a
// secret (SecretName), and every string shaped like one (SecretShaped), at any
// depth of nested maps and lists. A list loses a secret-shaped element rather
// than keeping a gap, and a map left empty by the filter is dropped. It is the
// one filter the dispatch payload and the engine apply to a device's vars
// property, so the two cannot disagree about what a template may read; the
// static inventory loader refuses such a value outright instead.
func CleanVars(vars map[string]any) map[string]any {
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		if SecretName(k) {
			continue
		}
		cleaned, keep := cleanVarValue(v)
		if keep {
			out[k] = cleaned
		}
	}
	return out
}

// cleanVarValue returns v without anything secret-shaped, and whether any of
// it is left.
func cleanVarValue(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		if SecretShaped(t) {
			return nil, false
		}
		return t, true
	case map[string]any:
		m := CleanVars(t)
		if len(m) == 0 && len(t) > 0 {
			return nil, false
		}
		return m, true
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			if c, keep := cleanVarValue(e); keep {
				out = append(out, c)
			}
		}
		if len(out) == 0 && len(t) > 0 {
			return nil, false
		}
		return out, true
	default:
		return v, true
	}
}
