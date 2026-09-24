// Package playbook: the operators of a translated condition, folding
// constants with Python's rules (which Ansible's Jinja follows) and
// building CEL for everything else.
package playbook

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// truth turns t into a bool: a constant by Python's truthiness, a
// registered field by its declared type, closed over every device.
func (c *celCtx) truth(t term) (term, *celError) {
	switch {
	case t.constant:
		return term{constant: true, value: pyTruthy(t.value)}, nil
	case t.register == "":
		if t.typ != "bool" {
			return term{}, unsupported("a value of type %s used as a condition", t.typ)
		}
		return t, nil
	}
	var pred string
	switch t.typ {
	case "bool":
		pred = t.cel
	case "string":
		pred = t.cel + ` != ""`
	case "int":
		pred = t.cel + " != 0"
	case "list":
		pred = "size(" + t.cel + ") > 0"
	default:
		return term{}, unsupported("%s.%s has no declared type, so its truth cannot be translated", t.register, t.field)
	}
	return term{cel: pred, typ: "bool"}, nil
}

// filter translates bool, int and length.
func (c *celCtx) filter(e *expr) (term, *celError) {
	in, err := c.eval(e.left)
	if err != nil {
		return term{}, err
	}
	if in.constant {
		switch e.name {
		case "bool":
			return term{constant: true, value: ansibleBool(in.value)}, nil
		case "int":
			i, ok := toInt(in.value)
			if !ok {
				return term{}, unsupported("int of %s that is not a number", kindOf(in.value))
			}
			return term{constant: true, value: i}, nil
		case "length":
			n, ok := length(in.value)
			if !ok {
				return term{}, unsupported("length of a value with no length")
			}
			return term{constant: true, value: int64(n)}, nil
		}
	}
	switch {
	case e.name == "length" && (in.typ == "string" || in.typ == "list"):
		return term{cel: "size(" + in.cel + ")", typ: "int", register: in.register, field: in.field}, nil
	case e.name == "int" && in.typ == "string":
		return term{cel: "int(" + in.cel + ")", typ: "int", register: in.register, field: in.field}, nil
	case e.name == "int" && in.typ == "int":
		return in, nil
	}
	return term{}, unsupported("filter %s on a registered %s", e.name, in.typ)
}

// defined translates is defined and is not defined.
func (c *celCtx) defined(e *expr) (term, *celError) {
	v := e.left
	if v.kind != exprVar {
		return term{}, unsupported("is defined on something other than a variable")
	}
	result := func(defined bool) (term, *celError) {
		return term{constant: true, value: defined != e.negate}, nil
	}
	switch {
	case isMagic(v.name):
		return term{}, &celError{"when.fact", fmt.Sprintf("%s is a fact or a magic variable", v.name)}
	case v.path == nil && hasBinding(c.bindings, v.name):
		return result(true)
	case c.registers[v.name].fqcn != "":
		if v.path != nil {
			return term{}, unsupported("is defined on a field of a registered result")
		}
		// Ansible registers a skipped task's result and this runtime does
		// not, so "defined" can only be false here where Ansible says
		// true. Asked positively, the task then does less; negated, it
		// would run where Ansible skips it, so that is refused.
		if e.negate != c.negated {
			return term{}, unsupported("%s is not defined: a skipped task registers a result in Ansible and none here, so the negation would run where Ansible skips", v.name)
		}
		c.readsRegister = true
		cel := strconv.Quote(v.name) + " in stat"
		if e.negate {
			cel = "!(" + cel + ")"
		}
		return term{cel: cel, typ: "bool"}, nil
	}
	if v.path != nil {
		return term{}, unsupported("is defined on a variable's field")
	}
	if _, rerr := c.r.ix.literal(v.name); rerr == nil {
		return result(true)
	}
	if len(c.r.ix.defs[v.name]) == 0 && c.r.ix.unknownSource == "" {
		return result(false)
	}
	return term{}, unsupported("whether %s is defined is known only at run time", v.name)
}

// logical translates and and or, folding a constant side away.
func (c *celCtx) logical(e *expr) (term, *celError) {
	l, err := c.toCEL(e.left)
	if err != nil {
		return term{}, err
	}
	r, err := c.toCEL(e.right)
	if err != nil {
		return term{}, err
	}
	and := e.kind == exprAnd
	switch {
	case l.constant && r.constant:
		if and {
			return term{constant: true, value: l.value.(bool) && r.value.(bool)}, nil
		}
		return term{constant: true, value: l.value.(bool) || r.value.(bool)}, nil
	case l.constant:
		if l.value.(bool) == and {
			return r, nil
		}
		return l, nil
	case r.constant:
		if r.value.(bool) == and {
			return l, nil
		}
		return r, nil
	}
	op := " || "
	if and {
		op = " && "
	}
	return term{cel: "(" + l.cel + ")" + op + "(" + r.cel + ")", typ: "bool"}, nil
}

// compare translates a comparison.
func (c *celCtx) compare(e *expr) (term, *celError) {
	l, err := c.eval(e.left)
	if err != nil {
		return term{}, err
	}
	r, err := c.eval(e.right)
	if err != nil {
		return term{}, err
	}
	if l.constant && r.constant {
		v, ok := pyCompare(e.op, l.value, r.value)
		if !ok {
			return term{}, unsupported("%s %s %s compares values of different kinds", kindOf(l.value), e.op, kindOf(r.value))
		}
		return term{constant: true, value: v}, nil
	}
	if !l.constant && !r.constant {
		return term{}, unsupported("a comparison between two registered results")
	}
	open, fixed, openLeft := l, r, true
	if l.constant {
		open, fixed, openLeft = r, l, false
	}
	if open.register == "" {
		// A whole condition compared with a constant can hide a negation
		// (x == false), which the defined rule must see.
		return term{}, unsupported("a condition compared with a constant; write the condition itself")
	}
	lit, lerr := celLiteral(fixed.value)
	if lerr != nil {
		return term{}, unsupported("%v", lerr)
	}
	var cel string
	switch e.op {
	case "==", "!=", "<", "<=", ">", ">=":
		if !compatible(open.typ, fixed.value) {
			return term{}, unsupported("%s.%s is a %s, compared with %s", open.register, open.field, open.typ, kindOf(fixed.value))
		}
		if openLeft {
			cel = open.cel + " " + e.op + " " + lit
		} else {
			cel = lit + " " + e.op + " " + open.cel
		}
	case "in", "not in":
		switch {
		case !openLeft && open.typ == "string" && isString(fixed.value):
			cel = open.cel + ".contains(" + lit + ")"
		case !openLeft && open.typ == "list":
			cel = lit + " in " + open.cel
		case openLeft && isList(fixed.value) && open.typ != "dyn":
			cel = open.cel + " in " + lit
		default:
			return term{}, unsupported("in between a %s and %s", open.typ, kindOf(fixed.value))
		}
		if e.op == "not in" {
			cel = "!(" + cel + ")"
		}
	}
	return term{cel: cel, typ: "bool"}, nil
}

// compatible reports whether a registered field of type typ can be
// compared with v in CEL without a type error.
func compatible(typ string, v any) bool {
	switch v.(type) {
	case int64, float64:
		return typ == "int" || typ == "float"
	case string:
		return typ == "string"
	case bool:
		return typ == "bool"
	}
	return false
}

// isString reports whether v is text.
func isString(v any) bool { _, ok := v.(string); return ok }

// isList reports whether v is a list.
func isList(v any) bool { _, ok := v.([]any); return ok }

// celLiteral renders a constant as a CEL literal, every string quoted.
func celLiteral(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case int:
		return strconv.Itoa(t), nil
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return "", fmt.Errorf("an infinite or NaN number has no CEL literal")
		}
		s := strconv.FormatFloat(t, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s, nil
	case string:
		return strconv.Quote(t), nil
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			lit, err := celLiteral(item)
			if err != nil {
				return "", err
			}
			parts[i] = lit
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			lit, err := celLiteral(t[k])
			if err != nil {
				return "", err
			}
			parts[i] = strconv.Quote(k) + ": " + lit
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	return "", fmt.Errorf("a value of type %T has no CEL literal", v)
}

// hasBinding reports whether name is bound, a loop item or index.
func hasBinding(bindings map[string]any, name string) bool {
	_, ok := bindings[name]
	return ok
}
