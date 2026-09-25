// Package playbook: parsing a when: condition, the Jinja expression
// subset this converter translates.
//
// Ansible evaluates when: as a Jinja expression per host; a runbook's
// when is CEL. Only a bounded subset is translated, parsed here into a
// small tree that whencel.go turns into CEL. The CEL is built from the
// tree alone, every string literal re-quoted, so no text from the
// playbook is ever pasted into an expression: a condition that could be
// spliced into something else is refused at parse instead. The subset:
// and, or, not, parentheses; ==, !=, <, <=, >, >=, in and not in; is
// defined and is not defined; literals; variables with .field and
// ['key'] access; and the filters bool, int and length.
package playbook

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// exprKind is the kind of one node of a parsed condition.
type exprKind int

const (
	exprLiteral exprKind = iota // a constant: value
	exprVar                     // a variable read: name, then path
	exprNot                     // not left
	exprAnd                     // left and right
	exprOr                      // left or right
	exprCompare                 // left op right
	exprDefined                 // left is (not) defined; negate says which
	exprFilter                  // left | name
	exprList                    // a list literal: items
)

// expr is one node of a parsed condition.
type expr struct {
	kind        exprKind
	value       any
	name        string
	path        []any // field names (string) and indexes (string or int64)
	op          string
	negate      bool
	left, right *expr
	items       []*expr
}

// whenParser is a recursive-descent parser over one condition's tokens.
type whenParser struct {
	toks []string
	pos  int
}

// parseWhen parses src, one condition.
func parseWhen(src string) (*expr, error) {
	if len(src) > maxWhenBytes {
		return nil, fmt.Errorf("condition is longer than %d bytes", maxWhenBytes)
	}
	toks, err := tokenizeWhen(src)
	if err != nil {
		return nil, err
	}
	p := &whenParser{toks: toks}
	e, err := p.or(0)
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unexpected %s", describeToken(p.toks[p.pos]))
	}
	return e, nil
}

// peek returns the next token, or "".
func (p *whenParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

// take consumes the next token when it is tok.
func (p *whenParser) take(tok string) bool {
	if p.peek() == tok {
		p.pos++
		return true
	}
	return false
}

// maxWhenNesting bounds how deeply parentheses and not may nest.
const maxWhenNesting = 32

// or parses a chain of or.
func (p *whenParser) or(depth int) (*expr, error) {
	if depth > maxWhenNesting {
		return nil, fmt.Errorf("condition nests too deeply")
	}
	left, err := p.and(depth)
	for err == nil && p.take("or") {
		var right *expr
		if right, err = p.and(depth); err == nil {
			left = &expr{kind: exprOr, left: left, right: right}
		}
	}
	return left, err
}

// and parses a chain of and.
func (p *whenParser) and(depth int) (*expr, error) {
	left, err := p.not(depth)
	for err == nil && p.take("and") {
		var right *expr
		if right, err = p.not(depth); err == nil {
			left = &expr{kind: exprAnd, left: left, right: right}
		}
	}
	return left, err
}

// not parses a prefix not.
func (p *whenParser) not(depth int) (*expr, error) {
	if p.take("not") {
		inner, err := p.not(depth + 1)
		if err != nil {
			return nil, err
		}
		return &expr{kind: exprNot, left: inner}, nil
	}
	return p.compare(depth)
}

// compare parses one optional comparison.
func (p *whenParser) compare(depth int) (*expr, error) {
	left, err := p.test(depth)
	if err != nil {
		return nil, err
	}
	op := p.peek()
	switch op {
	case "==", "!=", "<", "<=", ">", ">=", "in":
		p.pos++
	case "not":
		if p.pos+1 < len(p.toks) && p.toks[p.pos+1] == "in" {
			p.pos += 2
			op = "not in"
		} else {
			return left, nil
		}
	default:
		return left, nil
	}
	right, err := p.test(depth)
	if err != nil {
		return nil, err
	}
	return &expr{kind: exprCompare, op: op, left: left, right: right}, nil
}

// test parses a value, its filters, and an optional is (not) defined.
func (p *whenParser) test(depth int) (*expr, error) {
	e, err := p.primary(depth)
	if err != nil {
		return nil, err
	}
	for p.take("|") {
		name := p.peek()
		if name != "bool" && name != "int" && name != "length" {
			return nil, fmt.Errorf("filter %q is not one this converter translates", name)
		}
		p.pos++
		e = &expr{kind: exprFilter, name: name, left: e}
	}
	if p.take("is") {
		negate := p.take("not")
		switch p.peek() {
		case "defined":
			p.pos++
			return &expr{kind: exprDefined, negate: negate, left: e}, nil
		case "undefined":
			p.pos++
			return &expr{kind: exprDefined, negate: !negate, left: e}, nil
		}
		return nil, fmt.Errorf("test %s is not one this converter translates", describeToken(p.peek()))
	}
	return e, nil
}

// primary parses a literal, a variable read, a list or a parenthesized
// condition.
func (p *whenParser) primary(depth int) (*expr, error) {
	tok := p.peek()
	switch {
	case tok == "":
		return nil, fmt.Errorf("condition ends too soon")
	case tok == "(":
		p.pos++
		e, err := p.or(depth + 1)
		if err != nil {
			return nil, err
		}
		if !p.take(")") {
			return nil, fmt.Errorf("missing )")
		}
		return e, nil
	case tok == "[":
		p.pos++
		list := &expr{kind: exprList}
		for !p.take("]") {
			item, err := p.primary(depth + 1)
			if err != nil {
				return nil, err
			}
			list.items = append(list.items, item)
			if !p.take(",") && p.peek() != "]" {
				return nil, fmt.Errorf("expected , or ] in a list")
			}
		}
		return list, nil
	case tok[0] == '"' || tok[0] == '\'':
		p.pos++
		return &expr{kind: exprLiteral, value: tok[1 : len(tok)-1]}, nil
	case tok == "true" || tok == "True":
		p.pos++
		return &expr{kind: exprLiteral, value: true}, nil
	case tok == "false" || tok == "False":
		p.pos++
		return &expr{kind: exprLiteral, value: false}, nil
	case tok == "none" || tok == "None":
		p.pos++
		return &expr{kind: exprLiteral, value: nil}, nil
	case unicode.IsDigit(rune(tok[0])) || tok[0] == '-':
		p.pos++
		if i, err := strconv.ParseInt(tok, 10, 64); err == nil {
			return &expr{kind: exprLiteral, value: i}, nil
		}
		if f, err := strconv.ParseFloat(tok, 64); err == nil {
			return &expr{kind: exprLiteral, value: f}, nil
		}
		return nil, fmt.Errorf("a malformed number")
	case whenKeywords[tok]:
		return nil, fmt.Errorf("%q cannot stand where a value belongs", tok)
	case isIdent(tok):
		p.pos++
		return p.path(&expr{kind: exprVar, name: tok})
	}
	return nil, fmt.Errorf("unexpected %s", describeToken(tok))
}

// describeToken names tok for a refusal: an operator or a name as
// written, and a literal by its kind, since a literal is the playbook's
// value and a finding points at a value rather than printing it.
func describeToken(tok string) string {
	switch {
	case tok == "":
		return "the end"
	case tok[0] == '"' || tok[0] == '\'':
		return "a text literal"
	case unicode.IsDigit(rune(tok[0])) || (tok[0] == '-' && len(tok) > 1):
		return "a number"
	}
	return strconv.Quote(tok)
}

// path parses a variable's .field and [key] accesses.
func (p *whenParser) path(v *expr) (*expr, error) {
	for {
		switch {
		case p.take("."):
			field := p.peek()
			if !isIdent(field) {
				return nil, fmt.Errorf("expected a field name after a dot")
			}
			p.pos++
			v.path = append(v.path, field)
		case p.take("["):
			key := p.peek()
			p.pos++
			switch {
			case key != "" && (key[0] == '"' || key[0] == '\''):
				v.path = append(v.path, key[1:len(key)-1])
			default:
				i, err := strconv.ParseInt(key, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("an index must be a literal string or number")
				}
				v.path = append(v.path, i)
			}
			if !p.take("]") {
				return nil, fmt.Errorf("missing ]")
			}
		default:
			return v, nil
		}
	}
}

// whenKeywords are the grammar's own words, which are never a variable.
var whenKeywords = map[string]bool{"and": true, "or": true, "not": true, "in": true, "is": true, "defined": true, "undefined": true}

// isIdent reports whether s is a Jinja name.
func isIdent(s string) bool {
	if s == "" || !(unicode.IsLetter(rune(s[0])) || s[0] == '_') {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool { return !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) }) < 0
}
