package inventory

// Properties is a narrow typed accessor over a device's raw metadata. It
// exists so no caller performs an unchecked type assertion on a bare map
// at dispatch time; the accessor methods return an "ok" bool instead of
// panicking on a missing key or the wrong type.
type Properties struct {
	raw map[string]PropertyValue
}

// NewProperties wraps a raw property map. A nil map is treated as empty so
// callers never need a separate nil check before reading.
func NewProperties(raw map[string]PropertyValue) Properties {
	if raw == nil {
		raw = map[string]PropertyValue{}
	}
	return Properties{raw: raw}
}

// String returns the string value at key, or ("", false) if the key is
// absent or holds a different type.
func (p Properties) String(key string) (string, bool) {
	v, ok := p.raw[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// Int returns the int value at key. JSON and YAML both decode bare numbers
// as float64, so a float64 is accepted here too, not just a Go int.
func (p Properties) Int(key string) (int, bool) {
	v, ok := p.raw[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// Bool returns the bool value at key, or (false, false) if the key is
// absent or holds a different type.
func (p Properties) Bool(key string) (bool, bool) {
	v, ok := p.raw[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// Raw returns the underlying map for serialization (ShowInfo output, YAML
// round-tripping). Callers must treat the result as read-only.
func (p Properties) Raw() map[string]PropertyValue {
	return p.raw
}

// Len reports how many properties are set.
func (p Properties) Len() int {
	return len(p.raw)
}
