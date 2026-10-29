// Package render: the operators and tests of an expression (comparisons,
// in, and/or/not, ~ and the is tests), with Jinja's meaning and each
// charged to the render's step budget.
package render

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

// Comparisons, membership and tests, with Python's meaning, which is what
// Jinja2 evaluates them with: an int never equals a string, 1 equals 1.0
// and true (a bool is a number in Python), lists and mappings compare by
// content, and ordering two values Python cannot order (a number and a
// string, two mappings, null and anything) is an error rather than false.

// bytesPerStep is how many bytes of text one evaluation step pays for, so
// a filter, a ~ or a comparison over a large text spends budget in
// proportion to its work (maxEvaluations).
const bytesPerStep = 256

// testSpec is one test's arity and meaning. defined is handled by
// applyTest itself, since it acts on whether a path resolves rather than
// on a value.
type testSpec struct {
	arity int
	apply func(value any, args []any) bool
	usage string
	doc   string
}

// tests is the closed set of tests. A name absent from it is refused at
// compile time.
var tests = map[string]testSpec{
	"defined":  {usage: "x is defined", doc: "whether x was supplied; the one way besides default to read a name that may be missing"},
	"none":     {apply: func(v any, _ []any) bool { return v == nil }, usage: "x is none", doc: "whether x is null"},
	"string":   {apply: func(v any, _ []any) bool { _, ok := v.(string); return ok }, usage: "x is string", doc: "whether x is text"},
	"number":   {apply: func(v any, _ []any) bool { _, ok := number(v); return ok }, usage: "x is number", doc: "whether x is a number (true and false count, as in Python)"},
	"mapping":  {apply: func(v any, _ []any) bool { return isMapping(v) }, usage: "x is mapping", doc: "whether x is a mapping"},
	"iterable": {apply: func(v any, _ []any) bool { _, s := v.(string); return s || isList(v) || isMapping(v) }, usage: "x is iterable", doc: "whether a for loop can walk x: text, a list or a mapping"},
	"sameas":   {arity: 1, apply: sameAs, usage: "x is sameas false", doc: "whether x is the very same constant: true, false or none (any other value is never the same as anything)"},
}

// applyTest evaluates one "is" test.
func (n *exprNode) applyTest(vars env) (any, error) {
	var result bool
	if n.name == "defined" && n.operand.kind == exprRef {
		_, result = n.operand.resolve(vars)
	} else {
		value, err := n.operand.evaluate(vars)
		if err != nil {
			return nil, err
		}
		if n.name == "defined" {
			// Anything that evaluated is defined.
			result = true
		} else {
			args := make([]any, len(n.args))
			for i, a := range n.args {
				if args[i], err = a.evaluate(vars); err != nil {
					return nil, err
				}
			}
			result = tests[n.name].apply(value, args)
		}
	}
	return result != n.negate, nil
}

// sameAs is the sameas test: Python's "is", which this renderer can answer
// only for the constants true, false and none, since a value here has no
// identity of its own.
func sameAs(v any, args []any) bool {
	switch a := args[0].(type) {
	case nil:
		return v == nil
	case bool:
		b, ok := v.(bool)
		return ok && a == b
	}
	return false
}

// compare evaluates a chain of comparisons, stopping at the first false
// one so the operands after it are never read.
func (n *exprNode) compare(vars env) (any, error) {
	left, err := n.args[0].evaluate(vars)
	if err != nil {
		return nil, err
	}
	for i, op := range n.ops {
		right, err := n.args[i+1].evaluate(vars)
		if err != nil {
			return nil, err
		}
		ok, err := compareOne(vars, op, left, right)
		if err != nil {
			return nil, err
		}
		if !ok {
			return false, nil
		}
		left = right
	}
	return true, nil
}

// compareOne applies one comparison operator.
func compareOne(vars env, op string, a, b any) (bool, error) {
	switch op {
	case "==", "!=":
		eq, err := equal(vars, a, b)
		return eq == (op == "=="), err
	case "in", "not in":
		in, err := contains(vars, b, a)
		return in == (op == "in"), err
	}
	c, err := order(vars, a, b)
	if err != nil {
		return false, err
	}
	switch op {
	case "<":
		return c < 0, nil
	case "<=":
		return c <= 0, nil
	case ">":
		return c > 0, nil
	}
	return c >= 0, nil
}

// equal is Python's ==.
func equal(vars env, a, b any) (bool, error) {
	if err := vars.charge(1); err != nil {
		return false, err
	}
	if x, ok := number(a); ok {
		y, isNum := number(b)
		return isNum && x == y, nil
	}
	switch x := a.(type) {
	case nil:
		return b == nil, nil
	case string:
		y, ok := b.(string)
		return ok && x == y, vars.charge(len(x) / bytesPerStep)
	}
	if isList(a) && isList(b) {
		xs, ys := listItems(a), listItems(b)
		if len(xs) != len(ys) {
			return false, nil
		}
		for i := range xs {
			if eq, err := equal(vars, xs[i], ys[i]); err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	}
	if isMapping(a) && isMapping(b) {
		ra, rb := reflect.ValueOf(a), reflect.ValueOf(b)
		if ra.Len() != rb.Len() {
			return false, nil
		}
		for _, k := range ra.MapKeys() {
			vb := rb.MapIndex(reflect.ValueOf(k.String()).Convert(rb.Type().Key()))
			if !vb.IsValid() {
				return false, nil
			}
			if eq, err := equal(vars, ra.MapIndex(k).Interface(), vb.Interface()); err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	}
	return false, nil
}

// order is Python's ordering of a and b: negative, zero or positive, or an
// error for two values Python cannot order.
func order(vars env, a, b any) (int, error) {
	if err := vars.charge(1); err != nil {
		return 0, err
	}
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			switch {
			case x < y:
				return -1, nil
			case x > y:
				return 1, nil
			}
			return 0, nil
		}
	}
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			return strings.Compare(x, y), vars.charge(len(x) / bytesPerStep)
		}
	}
	if isList(a) && isList(b) {
		// The first pair that differs decides, and a list that is a prefix
		// of the other is the smaller.
		xs, ys := listItems(a), listItems(b)
		for i := 0; i < len(xs) && i < len(ys); i++ {
			eq, err := equal(vars, xs[i], ys[i])
			if err != nil {
				return 0, err
			}
			if !eq {
				return order(vars, xs[i], ys[i])
			}
		}
		return len(xs) - len(ys), nil
	}
	return 0, fmt.Errorf("%w: a %s and a %s cannot be ordered", ErrNotComparable, typeName(a), typeName(b))
}

// contains is Python's "needle in container": an element of a list, a key
// of a mapping, or a substring of text.
func contains(vars env, container, needle any) (bool, error) {
	switch c := container.(type) {
	case string:
		s, ok := needle.(string)
		if !ok {
			return false, fmt.Errorf("%w: in text needs text on its left, not a %s", ErrNotComparable, typeName(needle))
		}
		return strings.Contains(c, s), vars.charge(len(c) / bytesPerStep)
	case nil:
		return false, fmt.Errorf("%w: in needs a list, a mapping or text on its right, not null", ErrNotIterable)
	}
	switch {
	case isList(container):
		for _, item := range listItems(container) {
			if eq, err := equal(vars, needle, item); err != nil || eq {
				return eq, err
			}
		}
		return false, nil
	case isMapping(container):
		key, ok := needle.(string)
		if !ok {
			// A list or mapping cannot be a key in Python; anything else
			// simply is not one of these text keys.
			if isList(needle) || isMapping(needle) {
				return false, fmt.Errorf("%w: a %s cannot be a mapping's key", ErrNotComparable, typeName(needle))
			}
			return false, nil
		}
		rv := reflect.ValueOf(container)
		return rv.MapIndex(reflect.ValueOf(key).Convert(rv.Type().Key())).IsValid(), nil
	}
	return false, fmt.Errorf("%w: in needs a list, a mapping or text on its right, not a %s", ErrNotIterable, typeName(container))
}

// number returns v as a float64 when it is a number, a bool counting as 0
// or 1 as in Python. A float64 holds every integer a decoded document
// carries exactly up to 2^53, which is past any count or port.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case float64:
		return x, !math.IsNaN(x)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32:
		return rv.Float(), true
	}
	return 0, false
}

// isList reports whether v is a list.
func isList(v any) bool {
	if v == nil {
		return false
	}
	k := reflect.ValueOf(v).Kind()
	return k == reflect.Slice || k == reflect.Array
}

// isMapping reports whether v is a mapping with text keys.
func isMapping(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String
}

// listItems returns a list's elements.
func listItems(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	rv := reflect.ValueOf(v)
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out
}

// typeName names a value's type the way an author thinks of it, for an
// error message that must never quote the value.
func typeName(v any) string {
	switch {
	case v == nil:
		return "null"
	case isList(v):
		return "list"
	case isMapping(v):
		return "mapping"
	}
	if _, ok := v.(string); ok {
		return "text"
	}
	if _, ok := number(v); ok {
		return "number"
	}
	return fmt.Sprintf("%T", v)
}
