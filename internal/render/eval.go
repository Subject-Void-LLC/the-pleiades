package render

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// env is what an expression evaluates against: the names in scope, and
// the step budget that bounds the work one render may do.
type env interface {
	// lookup resolves a root name: the innermost name a statement bound,
	// then the caller's variables.
	lookup(name string) (any, bool)

	// charge spends n steps of the render's budget, refusing past it.
	charge(n int) error
}

// eval evaluates one expression and returns its rendered text.
func (e *expression) eval(vars env) (string, error) {
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

// evalValue evaluates e to its value, before any conversion to text: the
// half of eval that Value shares. An undefined name is returned as it is,
// so a caller can tell it with errors.Is; every other error names e.
func (e *expression) evalValue(vars env) (any, error) {
	value, err := e.body.evaluate(vars)
	if err != nil {
		if _, undefined := err.(*UndefinedError); undefined {
			return nil, err
		}
		return nil, fmt.Errorf("%w (in %q)", err, e.source)
	}
	return value, nil
}

// evaluate computes one node's value. This is where the strict-undefined
// rule described in the package comment is enforced, and where its two
// exceptions live: the default filter and the defined test, each applied
// directly to a path.
func (n *exprNode) evaluate(vars env) (any, error) {
	switch n.kind {
	case exprRef:
		value, defined := n.resolve(vars)
		if !defined {
			return nil, &UndefinedError{Name: n.reference()}
		}
		return value, nil

	case exprLiteral:
		return n.value, nil

	case exprList:
		out := make([]any, len(n.args))
		for i, a := range n.args {
			v, err := a.evaluate(vars)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil

	case exprMap:
		out := make(map[string]any, len(n.args))
		for i, a := range n.args {
			v, err := a.evaluate(vars)
			if err != nil {
				return nil, err
			}
			out[n.keys[i]] = v
		}
		return out, nil

	case exprMethod:
		v, err := n.operand.evaluate(vars)
		if err != nil {
			return nil, err
		}
		items, err := mappingItems(v, n.name)
		if err != nil {
			return nil, err
		}
		return items, vars.charge(len(items))

	case exprNot:
		v, err := n.operand.evaluate(vars)
		if err != nil {
			return nil, err
		}
		return !truthy(v), nil

	case exprAnd, exprOr:
		// Each operand is evaluated only if the ones before it did not
		// decide the answer, so a branch not taken reads nothing. As in
		// Jinja2 the result is the deciding operand, not a boolean.
		var v any
		for _, a := range n.args {
			var err error
			if v, err = a.evaluate(vars); err != nil {
				return nil, err
			}
			if truthy(v) == (n.kind == exprOr) {
				return v, nil
			}
		}
		return v, nil

	case exprCompare:
		return n.compare(vars)

	case exprConcat:
		return n.concat(vars)

	case exprFilter:
		return n.applyFilter(vars)

	case exprTest:
		return n.applyTest(vars)
	}
	return nil, fmt.Errorf("%w: unknown expression", ErrSyntax)
}

// concat joins its operands' text, as ~ does: the same text each would
// render as, so null, a list or a mapping is an error (use to_json).
func (n *exprNode) concat(vars env) (any, error) {
	var b strings.Builder
	for _, a := range n.args {
		v, err := a.evaluate(vars)
		if err != nil {
			return nil, err
		}
		s, err := text(v)
		if err != nil {
			return nil, err
		}
		b.WriteString(s)
		if b.Len() > maxOutputBytes {
			return nil, fmt.Errorf("%w: ~ built more than %d bytes", ErrTooLarge, maxOutputBytes)
		}
	}
	return b.String(), vars.charge(b.Len() / bytesPerStep)
}

// applyFilter applies one filter. default rescues only an undefined path
// it is applied to directly, which mirrors Jinja2:
// "{{ a | lower | default('x') }}" fails on an undefined a, because lower
// runs first and has nothing to lowercase. Accepting it anywhere would
// quietly change what a template imported from AWX evaluates to.
func (n *exprNode) applyFilter(vars env) (any, error) {
	if n.name == filterDefault {
		if n.operand.kind == exprRef {
			if value, defined := n.operand.resolve(vars); defined {
				return value, nil
			}
			return n.args[0].evaluate(vars)
		}
		// A default past the first position has nothing to do: the value
		// is defined by construction. It is accepted so an AWX template
		// using the redundant form still compiles.
		return n.operand.evaluate(vars)
	}
	value, err := n.operand.evaluate(vars)
	if err != nil {
		return nil, err
	}
	out, err := filters[n.name].apply(value, nil)
	if err != nil {
		return nil, err
	}
	// A filter's work grows with what it reads and writes; a loop must
	// not be able to repeat a megabyte's work ten thousand times.
	return out, vars.charge(workOf(value) + workOf(out))
}

// workOf is the step cost of handling value: one step per bytesPerStep of
// text.
func workOf(value any) int {
	if s, ok := value.(string); ok {
		return len(s) / bytesPerStep
	}
	return 0
}

// resolve walks a path through vars.
//
// The second return value distinguishes absent from present-and-null, which
// is the distinction the whole strict-undefined rule rests on. A key that
// is present with a nil value is defined, and rendering it is an error with
// its own message, because a caller who explicitly supplied null meant
// something different from a caller who supplied nothing.
func (n *exprNode) resolve(vars env) (any, bool) {
	current, ok := vars.lookup(n.root)
	if !ok {
		return nil, false
	}
	for _, s := range n.steps {
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
