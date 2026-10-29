// Package render: parsing an expression's primaries: a reference path,
// a literal, a list or mapping, and a parenthesized expression.
package render

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The primary expressions: literals, paths, and bracketed forms.

// methods are the mapping methods a path may end with, each returning a
// list: .items() its [key, value] pairs, .keys() its keys and .values() its
// values, all in sorted key order.
var methods = map[string]bool{"items": true, "keys": true, "values": true}

// parsePrimary reads one literal, path or bracketed expression.
func (s *scanner) parsePrimary() (*exprNode, error) {
	s.skipSpace()
	if s.done() {
		return nil, fmt.Errorf("%w: expected a value", ErrSyntax)
	}
	switch c := s.peek(); {
	case c == '\'' || c == '"':
		text, err := s.quoted()
		if err != nil {
			return nil, err
		}
		return &exprNode{kind: exprLiteral, value: text}, nil
	case c >= '0' && c <= '9':
		return s.number()
	case c == '(':
		return s.parenthesized()
	case c == '[':
		s.pos++
		items, err := s.arguments("]")
		if err != nil {
			return nil, err
		}
		return &exprNode{kind: exprList, args: items}, nil
	case c == '{':
		return s.mapping()
	}

	word := s.peekWord()
	if value, ok := constants[word]; ok {
		s.pos += len(word)
		return &exprNode{kind: exprLiteral, value: value}, nil
	}
	if keywords[word] {
		return nil, fmt.Errorf("%w: expected a value, found %q", ErrSyntax, word)
	}
	return s.reference()
}

// reference reads a path and an optional method call after it.
func (s *scanner) reference() (*exprNode, error) {
	root, err := s.ident()
	if err != nil {
		return nil, err
	}
	steps, err := s.path()
	if err != nil {
		return nil, err
	}
	node := &exprNode{kind: exprRef, root: root, steps: steps}

	// "m.items()" reads as the path m.items followed by a call: the last
	// key is the method's name.
	if !s.done() && s.peek() == '(' {
		last := len(steps) - 1
		if last < 0 || steps[last].kind != stepKey || !methods[steps[last].key] {
			return nil, fmt.Errorf("%w: calling %s is not supported; a mapping has .items(), .keys() and .values()", ErrSyntax, node.reference())
		}
		name := steps[last].key
		node.steps = steps[:last]
		s.pos++
		if !s.acceptSymbol(")") {
			return nil, fmt.Errorf("%w: .%s() takes no arguments", ErrSyntax, name)
		}
		return &exprNode{kind: exprMethod, operand: node, name: name}, nil
	}
	return node, nil
}

// path reads the descent steps that follow a root identifier.
func (s *scanner) path() ([]step, error) {
	var steps []step
	for !s.done() {
		switch s.peek() {
		case '.':
			s.pos++
			name, err := s.ident()
			if err != nil {
				return nil, err
			}
			steps = append(steps, step{kind: stepKey, key: name})
		case '[':
			s.pos++
			st, err := s.subscript()
			if err != nil {
				return nil, err
			}
			steps = append(steps, st)
		default:
			return steps, nil
		}
		if len(steps) > maxPathDepth {
			return nil, fmt.Errorf("%w: a path deeper than %d steps", ErrTooLarge, maxPathDepth)
		}
	}
	return steps, nil
}

// subscript reads one bracketed key or index, with the opening bracket
// already consumed. It is literal on purpose: a computed subscript would
// make what a template reads unknowable until it runs.
func (s *scanner) subscript() (step, error) {
	s.skipSpace()
	var st step
	switch {
	case s.done():
		return st, fmt.Errorf("%w: unterminated %q", ErrSyntax, "[")
	case s.peek() == '\'' || s.peek() == '"':
		key, err := s.quoted()
		if err != nil {
			return st, err
		}
		st = step{kind: stepKey, key: key}
	default:
		n, err := s.integer()
		if err != nil {
			return st, fmt.Errorf("%w (a subscript is a literal key or index)", err)
		}
		st = step{kind: stepIndex, index: n}
	}
	s.skipSpace()
	if s.done() || s.peek() != ']' {
		return st, fmt.Errorf("%w: unterminated %q", ErrSyntax, "[")
	}
	s.pos++
	return st, nil
}

// parenthesized reads "(" expr ")", or a tuple, which is a list here:
// "(a, b)", "(a,)" or "()".
func (s *scanner) parenthesized() (*exprNode, error) {
	s.pos++
	if err := s.enter(); err != nil {
		return nil, err
	}
	defer s.leave()
	if s.acceptSymbol(")") {
		return &exprNode{kind: exprList}, nil
	}
	first, err := s.parseOr()
	if err != nil {
		return nil, err
	}
	if s.acceptSymbol(")") {
		return first, nil
	}
	if !s.acceptSymbol(",") {
		return nil, fmt.Errorf("%w: unterminated %q", ErrSyntax, "(")
	}
	rest, err := s.arguments(")")
	if err != nil {
		return nil, err
	}
	if len(rest)+1 > maxLiteralItems {
		return nil, fmt.Errorf("%w: more than %d items in one list", ErrTooLarge, maxLiteralItems)
	}
	return &exprNode{kind: exprList, args: append([]*exprNode{first}, rest...)}, nil
}

// mapping reads a mapping literal, with text keys: {'a': 1, "b": x}.
func (s *scanner) mapping() (*exprNode, error) {
	s.pos++
	if err := s.enter(); err != nil {
		return nil, err
	}
	defer s.leave()
	node := &exprNode{kind: exprMap}
	seen := map[string]bool{}
	for {
		if s.acceptSymbol("}") {
			return node, nil
		}
		s.skipSpace()
		if s.done() || (s.peek() != '\'' && s.peek() != '"') {
			return nil, fmt.Errorf("%w: a mapping literal's keys are quoted text", ErrSyntax)
		}
		key, err := s.quoted()
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, fmt.Errorf("%w: a mapping literal names %q twice", ErrSyntax, key)
		}
		seen[key] = true
		if !s.acceptSymbol(":") {
			return nil, fmt.Errorf("%w: expected : after a mapping key", ErrSyntax)
		}
		value, err := s.parseOr()
		if err != nil {
			return nil, err
		}
		node.keys = append(node.keys, key)
		node.args = append(node.args, value)
		if len(node.args) > maxLiteralItems {
			return nil, fmt.Errorf("%w: more than %d items in one mapping", ErrTooLarge, maxLiteralItems)
		}
		if s.acceptSymbol("}") {
			return node, nil
		}
		if !s.acceptSymbol(",") {
			return nil, fmt.Errorf("%w: unterminated mapping, expected , or }", ErrSyntax)
		}
	}
}

// number reads an integer or float literal in Jinja2's spelling: decimal
// with optional single underscores between digits, 0b, 0o or 0x integers,
// and floats with a fraction, an exponent or both. A decimal integer may
// not start with 0, as in Jinja2 and Python 3.
func (s *scanner) number() (*exprNode, error) {
	start := s.pos
	for !s.done() && (isIdentPart(s.peek()) || s.peek() == '.' ||
		((s.peek() == '+' || s.peek() == '-') && (s.src[s.pos-1] == 'e' || s.src[s.pos-1] == 'E'))) {
		// A "." belongs to the number only when a digit follows it, so
		// "1.x" is refused as a number rather than read as a path.
		if s.peek() == '.' && (s.pos+1 >= len(s.src) || s.src[s.pos+1] < '0' || s.src[s.pos+1] > '9') {
			break
		}
		s.pos++
	}
	text := s.src[start:s.pos]
	if bad := strings.Contains(text, "__") || strings.HasSuffix(text, "_") || strings.Contains(text, "_.") || strings.Contains(text, "._"); bad {
		return nil, fmt.Errorf("%w: %q is not a number", ErrSyntax, text)
	}
	plain := strings.ReplaceAll(text, "_", "")
	lower := strings.ToLower(plain)

	isFloat := !strings.HasPrefix(lower, "0x") && strings.ContainsAny(lower, ".e")
	if isFloat {
		f, err := strconv.ParseFloat(plain, 64)
		if err != nil || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%w: %q is not a number in range", ErrSyntax, text)
		}
		return &exprNode{kind: exprLiteral, value: f}, nil
	}
	base := 10
	digits := lower
	switch {
	case strings.HasPrefix(lower, "0x"):
		base, digits = 16, lower[2:]
	case strings.HasPrefix(lower, "0o"):
		base, digits = 8, lower[2:]
	case strings.HasPrefix(lower, "0b"):
		base, digits = 2, lower[2:]
	case len(lower) > 1 && lower[0] == '0' && strings.Trim(lower, "0") != "":
		return nil, fmt.Errorf("%w: %q: a decimal number may not start with 0", ErrSyntax, text)
	}
	n, err := strconv.ParseInt(digits, base, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not a number in range", ErrSyntax, text)
	}
	return &exprNode{kind: exprLiteral, value: n}, nil
}
