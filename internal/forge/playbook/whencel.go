// Package playbook: turning a parsed when: condition into CEL.
//
// A variable the playbook fixes (a loop item, a variable with one literal
// value) is folded into a constant, with Jinja's own truthiness and
// comparison rules, so a condition over them becomes true or false. A
// registered result becomes a read of the native result under stat:, one
// entry per device, which a native condition reads once for the task
// rather than once per host, so the read is wrapped in all() and the
// report asks a person to confirm it. Everything else is refused.
package playbook

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// produced is what a converted task registered: the native method whose
// result the name holds, and how Ansible's result fields map onto it.
type produced struct {
	fqcn    string
	returns map[string]string
}

// celCtx translates one condition.
type celCtx struct {
	r         *resolver
	bindings  map[string]any
	use       Position
	registers map[string]produced
	// readsRegister is set when the condition reads a registered result,
	// and perDevice holds the registers it reads one device at a time as
	// stat.r[d], which condition quantifies over once.
	readsRegister bool
	perDevice     map[string]bool
	// negated is set while translating under an odd number of nots.
	negated bool
}

// term is a condition node's translation: a constant, or CEL text of a
// known type ("bool", "string", "int", "list", or "dyn").
type term struct {
	constant bool
	value    any
	cel      string
	typ      string
	// register is set for a single registered field, and field is the
	// native field it reads.
	register, field string
}

// celError says why a condition cannot be translated, and which code.
type celError struct {
	code Code
	why  string
}

// Error implements error.
func (e *celError) Error() string { return e.why }

// unsupported builds a when.unsupported refusal.
func unsupported(format string, args ...any) *celError {
	return &celError{"when.unsupported", fmt.Sprintf(format, args...)}
}

// toCEL translates e into one CEL expression of type bool, or a constant
// bool the caller can fold away.
func (c *celCtx) toCEL(e *expr) (term, *celError) {
	t, err := c.eval(e)
	if err != nil {
		return term{}, err
	}
	return c.truth(t)
}

// eval translates any node.
func (c *celCtx) eval(e *expr) (term, *celError) {
	switch e.kind {
	case exprLiteral:
		return term{constant: true, value: e.value}, nil
	case exprList:
		list := []any{}
		for _, item := range e.items {
			t, err := c.eval(item)
			if err != nil {
				return term{}, err
			}
			if !t.constant {
				return term{}, unsupported("a list holding a registered result")
			}
			list = append(list, t.value)
		}
		return term{constant: true, value: list}, nil
	case exprVar:
		return c.variable(e)
	case exprFilter:
		return c.filter(e)
	case exprDefined:
		return c.defined(e)
	case exprNot:
		c.negated = !c.negated
		t, err := c.toCEL(e.left)
		c.negated = !c.negated
		if err != nil || t.constant {
			return term{constant: true, value: err == nil && !t.value.(bool)}, err
		}
		return term{cel: "!(" + t.cel + ")", typ: "bool"}, nil
	case exprAnd, exprOr:
		return c.logical(e)
	case exprCompare:
		return c.compare(e)
	}
	return term{}, unsupported("an expression this converter does not translate")
}

// variable translates a variable read: a constant when the playbook fixes
// it, a registered field when a converted task produced it.
func (c *celCtx) variable(e *expr) (term, *celError) {
	if p, ok := c.registers[e.name]; ok {
		return c.registered(e, p)
	}
	v, rerr := c.r.value(e.name, c.bindings, c.use, 0)
	if rerr != nil {
		code := rerr.code
		switch code {
		case "template.unresolved":
			code = "when.unsupported"
		case "template.fact":
			code = "when.fact"
		}
		return term{}, &celError{code, rerr.why}
	}
	for _, step := range e.path {
		next, ok := index(v, step)
		if !ok {
			return term{}, unsupported("%s has no %v", e.name, step)
		}
		v = next
	}
	return term{constant: true, value: v}, nil
}

// registered translates a read of one field of a registered result.
func (c *celCtx) registered(e *expr, p produced) (term, *celError) {
	if len(e.path) != 1 {
		return term{}, unsupported("%s is read as a whole or more than one field deep; only one field of a registered result translates", e.name)
	}
	field, ok := e.path[0].(string)
	if !ok {
		return term{}, unsupported("%s is indexed by number", e.name)
	}
	native, ok := p.returns[field]
	if !ok {
		return term{}, unsupported("%s.%s: %s returns no equivalent of Ansible's %s", e.name, field, p.fqcn, field)
	}
	typ := "dyn"
	if d, ok := collection.Lookup(p.fqcn); ok {
		for _, r := range d.Manifest.Doc.Returns {
			if r.Name == native {
				typ = r.Type
			}
		}
	}
	c.readsRegister = true
	c.perDevice[e.name] = true
	return term{cel: "stat" + celKey(e.name) + "[d]" + celKey(native), typ: typ, register: e.name, field: native}, nil
}

// wrapAll wraps cel, which reads register r per device as stat.r[d], in
// stat.r.all(d, ...): the condition holds when it holds on every device.
// A register a skipped task never wrote is absent from stat, so the read
// fails the run, as Ansible's read of a skipped result's field does.
func wrapAll(register, cel string) string {
	return "stat" + celKey(register) + ".all(d, " + cel + ")"
}

// celIdent matches a name CEL can read with a dot.
var celIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// celReserved are CEL's reserved words, which a dot cannot read.
var celReserved = map[string]bool{"true": true, "false": true, "null": true, "in": true, "as": true, "break": true, "const": true, "continue": true, "else": true, "for": true, "function": true, "if": true, "import": true, "let": true, "loop": true, "package": true, "namespace": true, "return": true, "var": true, "void": true, "while": true}

// celKey renders a map key read: .name when name is a plain identifier,
// ["name"] otherwise, quoted so the name cannot become CEL syntax.
func celKey(name string) string {
	if celIdent.MatchString(name) && !celReserved[name] {
		return "." + name
	}
	return "[" + strconv.Quote(name) + "]"
}

// index reads step from v, a map or a list, as Jinja would.
func index(v any, step any) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		key, ok := step.(string)
		if !ok {
			return nil, false
		}
		x, ok := t[key]
		return x, ok
	case []any:
		i, ok := step.(int64)
		if !ok || i < 0 || i >= int64(len(t)) {
			return nil, false
		}
		return t[i], true
	}
	return nil, false
}
