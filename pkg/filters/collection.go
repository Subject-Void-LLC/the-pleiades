package filters

import "reflect"

// DropEmptyValues returns a copy of m with every key whose value is nil,
// an empty string, an empty list, or an empty map removed. It does not
// treat a falsy-but-present value (0, false) as empty: this mirrors "drop
// blank/missing", not "drop falsy", the same distinction Ansible's own
// selectattr filters draw. It is not recursive: a nested map's own empty
// values are left alone, matching Pluck/ShallowMerge's own "one level"
// convention elsewhere in this package rather than Flatten/DeepMerge's
// recursive one.
func DropEmptyValues(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if isEmptyValue(v) {
			continue
		}
		out[k] = v
	}
	return out
}

func isEmptyValue(v any) bool {
	switch vv := v.(type) {
	case nil:
		return true
	case string:
		return vv == ""
	case []any:
		return len(vv) == 0
	case map[string]any:
		return len(vv) == 0
	default:
		return false
	}
}

// FilterListByKV keeps only the maps in list whose key equals value. A
// map missing key entirely never matches. See valuesEqual for how
// equality is decided.
func FilterListByKV(list []map[string]any, key string, value any) []map[string]any {
	var out []map[string]any
	for _, m := range list {
		if v, ok := m[key]; ok && valuesEqual(v, value) {
			out = append(out, m)
		}
	}
	return out
}

// ExcludeListByKV keeps only the maps in list whose key does not equal
// value, including every map missing key entirely (a missing key is not
// a match, so it survives): the exact converse of FilterListByKV, so
// that filtering a list with FilterListByKV and ExcludeListByKV on the
// same key and value always partitions it with no overlap and no gap.
func ExcludeListByKV(list []map[string]any, key string, value any) []map[string]any {
	var out []map[string]any
	for _, m := range list {
		v, ok := m[key]
		if !ok || !valuesEqual(v, value) {
			out = append(out, m)
		}
	}
	return out
}

// ListContains reports whether list contains an element equal to value.
// See valuesEqual for how equality is decided.
func ListContains(list []any, value any) bool {
	return containsValue(list, value)
}

// HasMandatoryTags returns the subset of requiredKeys that are absent
// from m, so an empty result means every mandatory key is present. This
// is a presence check, not an emptiness one: a key present with an empty
// or zero value is not reported missing here (that is DropEmptyValues'
// concern, a separate operation), only a key that does not exist in m at
// all.
func HasMandatoryTags(m map[string]any, requiredKeys []string) []string {
	var missing []string
	for _, k := range requiredKeys {
		if _, ok := m[k]; !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

// ListIntersect returns each element of a that also appears in b,
// preserving a's own order and multiplicity: an element appearing twice
// in a and once in b appears twice in the result. It does not
// deduplicate; DedupeByKey exists as its own explicit operation for that,
// scoped to a list of maps by one key rather than to a bare value list.
func ListIntersect(a, b []any) []any {
	var out []any
	for _, v := range a {
		if containsValue(b, v) {
			out = append(out, v)
		}
	}
	return out
}

// ListDiff returns each element of a that does not appear in b,
// preserving a's own order and multiplicity, the same non-deduplicating
// convention ListIntersect uses.
func ListDiff(a, b []any) []any {
	var out []any
	for _, v := range a {
		if !containsValue(b, v) {
			out = append(out, v)
		}
	}
	return out
}

// DedupeByKey keeps only the first map in list for each distinct value
// of key, preserving list's own order. A map missing key entirely is
// never deduplicated against anything (there is no value to compare),
// so every such map is kept.
func DedupeByKey(list []map[string]any, key string) []map[string]any {
	var out []map[string]any
	var seenKeys []any
	for _, m := range list {
		v, ok := m[key]
		if ok {
			if containsValue(seenKeys, v) {
				continue
			}
			seenKeys = append(seenKeys, v)
		}
		out = append(out, m)
	}
	return out
}

func containsValue(list []any, v any) bool {
	for _, item := range list {
		if valuesEqual(item, v) {
			return true
		}
	}
	return false
}

// valuesEqual reports whether a and b are equal for this file's own
// KV/contains/dedupe comparisons. Any two numeric values (int64, int, or
// float64, however this package's own celToAny conversion happened to
// produce them) compare by numeric value rather than by Go type first: a
// device fact decoded through JSON as a bare number and a runbook
// literal typed as a CEL int would otherwise never compare equal under
// reflect.DeepEqual alone, silently defeating every function in this
// file. Anything else falls back to reflect.DeepEqual, which already
// does the right thing for strings, bools, nil, and nested maps/lists.
func valuesEqual(a, b any) bool {
	if af, ok := toFloat64(a); ok {
		if bf, ok := toFloat64(b); ok {
			return af == bf
		}
	}
	return reflect.DeepEqual(a, b)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
