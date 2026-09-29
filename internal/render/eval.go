package render

import (
	"fmt"
	"reflect"
	"strconv"
)

// eval resolves one expression against vars and returns its rendered text.
//
// This is where the strict-undefined rule described in the package comment
// is actually enforced, and where its single exception lives.
func (e *expression) eval(vars map[string]any) (string, error) {
	value, err := e.evalValue(vars)
	if err != nil {
		return "", err
	}
	out, err := text(value)
	if err != nil {
		return "", fmt.Errorf("%w (in %q)", err, e.source)
	}
	return out, nil
}

// evalValue resolves e's path and applies its filters, returning the
// value before any conversion to text: the half of eval that Value shares.
func (e *expression) evalValue(vars map[string]any) (any, error) {
	value, defined := e.resolve(vars)
	chain := e.filters

	// The default filter is the only escape from strict-undefined, and it
	// rescues only when it is the first filter in the chain. That mirrors
	// Jinja2: "{{ a | lower | default('x') }}" fails on an undefined a,
	// because lower runs first and has nothing to lowercase. Accepting it
	// anywhere in the chain would quietly change what a template imported
	// from AWX evaluates to.
	if len(chain) > 0 && chain[0].name == filterDefault {
		if !defined {
			value = chain[0].args[0]
			defined = true
		}
		chain = chain[1:]
	}

	if !defined {
		return nil, &UndefinedError{Name: e.reference()}
	}

	for _, f := range chain {
		// A default past the first position has nothing to do: the value
		// is defined by construction at this point. It is accepted rather
		// than refused so that an AWX template using the redundant form
		// still compiles.
		if f.name == filterDefault {
			continue
		}

		var err error
		value, err = filters[f.name].apply(value, f.args)
		if err != nil {
			return nil, fmt.Errorf("%w (in %q)", err, e.source)
		}
	}
	return value, nil
}

// resolve walks the expression's path through vars.
//
// The second return value distinguishes absent from present-and-null, which
// is the distinction the whole strict-undefined rule rests on. A key that
// is present with a nil value is defined, and rendering it is an error with
// its own message, because a caller who explicitly supplied null meant
// something different from a caller who supplied nothing.
func (e *expression) resolve(vars map[string]any) (any, bool) {
	current, ok := vars[e.root]
	if !ok {
		return nil, false
	}

	for _, s := range e.steps {
		current, ok = descend(current, s)
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// descend applies one path step to a value.
//
// A step that does not fit the value's shape (a key read from a string, an
// index past the end of a list) reports undefined rather than an error, so
// the default filter can rescue it and so the message a caller sees names
// the whole reference rather than an internal type.
func descend(value any, s step) (any, bool) {
	switch s.kind {
	case stepKey:
		// The two fast paths are the shapes this codebase actually
		// produces: a decoded JSON document and a credential's flat input
		// map. The reflect fallback covers everything else without needing
		// a case per map type.
		switch m := value.(type) {
		case map[string]any:
			v, ok := m[s.key]
			return v, ok
		case map[string]string:
			v, ok := m[s.key]
			return v, ok
		}
		return descendByReflection(value, s)

	case stepIndex:
		switch l := value.(type) {
		case []any:
			if s.index < 0 || s.index >= len(l) {
				return nil, false
			}
			return l[s.index], true
		case []string:
			if s.index < 0 || s.index >= len(l) {
				return nil, false
			}
			return l[s.index], true
		}
		return descendByReflection(value, s)
	}

	return nil, false
}

// descendByReflection handles map and slice shapes the fast paths miss.
func descendByReflection(value any, s step) (any, bool) {
	if value == nil {
		return nil, false
	}
	rv := reflect.ValueOf(value)

	switch rv.Kind() {
	case reflect.Map:
		if s.kind != stepKey || rv.Type().Key().Kind() != reflect.String {
			return nil, false
		}
		found := rv.MapIndex(reflect.ValueOf(s.key))
		if !found.IsValid() {
			return nil, false
		}
		return found.Interface(), true

	case reflect.Slice, reflect.Array:
		if s.kind != stepIndex || s.index < 0 || s.index >= rv.Len() {
			return nil, false
		}
		return rv.Index(s.index).Interface(), true
	}

	return nil, false
}

// text converts a resolved value to the string this renderer will emit.
//
// It is deliberately narrow. A map or a slice reaching here means the
// author wrote a reference to a structure and did not ask for to_json, and
// Go's default formatting of a map is not valid input to a shell, an
// environment variable, or Ansible. Emitting it would produce a value that
// looks like data and parses as nothing.
func text(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		// Lowercase because that is what both JSON and Ansible expect. Go's
		// own strconv.FormatBool already agrees.
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int8, int16, int32, int64:
		return strconv.FormatInt(reflect.ValueOf(v).Int(), 10), nil
	case uint, uint8, uint16, uint32, uint64:
		return strconv.FormatUint(reflect.ValueOf(v).Uint(), 10), nil
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), nil
	case float64:
		// 'f' with precision -1 gives the shortest form that round-trips,
		// so a whole number supplied as a float renders as "3" rather than
		// "3e+00", which is what a port number or a retry count needs.
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case nil:
		return "", fmt.Errorf("%w: the value is null, which is not the empty string", ErrNotRenderable)
	}

	return "", fmt.Errorf("%w: a %T has no text form, use the to_json filter", ErrNotRenderable, value)
}
