package launch

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Fields is a sparse set of values keyed by field name.
//
// Sparse is the whole contract: a key that is absent means "not supplied",
// which is a different instruction from a key present with an empty value.
// Collapsing the two is the same defect a PATCH has when it cannot tell an
// omitted list from an explicit empty one, recorded here as
// FAILURE_PATTERNS.md #103, where renaming a record silently emptied its
// membership. A launch that omits `limit` inherits the template's; a launch
// that sets `limit` to the empty string is asking to run against everything
// the inventory holds, and those are different runs.
//
// A map rather than a struct, and that follows from the open Kind. A struct
// would need a field per launch field, which cannot be written for kinds
// this package has never seen. What keeps it typed is the kind's own
// FieldSpec list: every value here is checked against a declared type
// before it is stored, and the typed accessors below are the only way back
// out.
type Fields map[string]any

// Has reports whether a value was supplied for name.
func (f Fields) Has(name string) bool {
	if f == nil {
		return false
	}
	_, ok := f[name]
	return ok
}

// Names lists the supplied fields in a stable order, so an error message or
// an ignored-field report reads the same way twice.
func (f Fields) Names() []string {
	out := make([]string, 0, len(f))
	for name := range f {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Clone returns a copy, so folding one layer over another never mutates a
// caller's map. A saved launch configuration is shared by every launch that
// uses it, and a fold that wrote through would let one launch's overrides
// leak into the next.
func (f Fields) Clone() Fields {
	if f == nil {
		return nil
	}
	out := make(Fields, len(f))
	for name, value := range f {
		if nested, ok := value.(map[string]any); ok {
			out[name] = cloneMap(nested)
			continue
		}
		if list, ok := value.([]string); ok {
			out[name] = append([]string(nil), list...)
			continue
		}
		out[name] = value
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// String reads a TypeString field, empty when absent.
func (f Fields) String(name string) string {
	s, _ := f[name].(string)
	return s
}

// Int reads a TypeInt field, zero when absent.
//
// It accepts the several shapes an integer arrives in across this
// platform's boundaries: a Go int from code, a float64 from encoding/json,
// and a string from an HTML form. Every one of those is a real caller, and
// a reader that only handled the first would work in tests and fail on the
// wire.
func (f Fields) Int(name string) int {
	switch v := f[name].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// List reads a TypeStringList field, nil when absent.
func (f Fields) List(name string) []string {
	switch v := f[name].(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}

// Map reads a TypeMap field, nil when absent.
func (f Fields) Map(name string) map[string]any {
	v, _ := f[name].(map[string]any)
	if v == nil {
		return nil
	}
	return cloneMap(v)
}

// normalize checks a supplied value against a field's declared type and
// returns it in that type's canonical Go shape.
//
// Canonicalising here, once, is what lets everything downstream read a
// value without asking which boundary it arrived over. The alternative is
// every consumer handling float64-or-int-or-string, and the one that
// forgets is the one that silently reads zero.
func normalize(spec FieldSpec, value any) (any, error) {
	switch spec.Type {
	case TypeString:
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %q must be text", ErrInvalidField, spec.Name)
		}
		return s, nil

	case TypeInt:
		n, err := toInt(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %q must be a whole number", ErrInvalidField, spec.Name)
		}
		if spec.Bounded() && (n < spec.Min || n > spec.Max) {
			return nil, fmt.Errorf("%w: %q must be between %d and %d, got %d",
				ErrInvalidField, spec.Name, spec.Min, spec.Max, n)
		}
		return n, nil

	case TypeStringList:
		list, err := toStringList(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %q must be a list of text values", ErrInvalidField, spec.Name)
		}
		return list, nil

	case TypeMap:
		m, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: %q must be a set of key and value pairs", ErrInvalidField, spec.Name)
		}
		return cloneMap(m), nil

	case TypeChoice:
		s, ok := value.(string)
		if !ok || !slices.Contains(spec.Choices, s) {
			return nil, fmt.Errorf("%w: %q must be one of %s, got %v", ErrInvalidField, spec.Name, strings.Join(spec.Choices, ", "), value)
		}
		return s, nil

	default:
		return nil, fmt.Errorf("%w: %q declares unknown type %q", ErrInvalidField, spec.Name, spec.Type)
	}
}

func toInt(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		// A JSON number that is not whole is refused rather than
		// truncated: "forks: 4.7" is a caller who has misunderstood
		// something, and silently running four would hide it.
		if v != float64(int(v)) {
			return 0, fmt.Errorf("not a whole number")
		}
		return int(v), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(v))
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func toStringList(value any) ([]string, error) {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("not a list of text")
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("not a list")
	}
}
