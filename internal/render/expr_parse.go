// Package render: parsing an expression's operators, from or down to the
// filters and tests that bind tighter than all of them.
package render

import (
	"fmt"
	"strings"
)

// The precedence-climbing half of the expression parser (expr.go has the
// grammar). Each binary level is a loop rather than a recursion, so a long
// chain of "and" or "~" builds one flat node and never a deep tree; only a
// nested sub-expression recurses, and maxExprDepth bounds that.

// keywords are names the grammar reserves, which a path may not start
// with.
var keywords = map[string]bool{
	"and": true, "or": true, "not": true, "in": true, "is": true, "if": true, "else": true,
}

// constants are the literal names, in both of Jinja2's spellings.
var constants = map[string]any{
	"true": true, "True": true, "false": false, "False": false, "none": nil, "None": nil,
}

// comparisons are the symbol operators, longest first so "<=" is not read
// as "<".
var comparisons = []string{"==", "!=", "<=", ">=", "<", ">"}

// peekWord returns the identifier under the cursor without consuming it,
// or "".
func (s *scanner) peekWord() string {
	end := s.pos
	if end >= len(s.src) || !isIdentStart(s.src[end]) {
		return ""
	}
	for end < len(s.src) && isIdentPart(s.src[end]) {
		end++
	}
	return s.src[s.pos:end]
}

// acceptWord consumes word when it is the identifier under the cursor.
func (s *scanner) acceptWord(word string) bool {
	s.skipSpace()
	if s.peekWord() != word {
		return false
	}
	s.pos += len(word)
	return true
}

// acceptSymbol consumes sym when it is under the cursor.
func (s *scanner) acceptSymbol(sym string) bool {
	s.skipSpace()
	if !strings.HasPrefix(s.src[s.pos:], sym) {
		return false
	}
	s.pos += len(sym)
	return true
}

// parseOr reads and ("or" and)*.
func (s *scanner) parseOr() (*exprNode, error) {
	return s.parseChain("or", exprOr, s.parseAnd)
}

// parseAnd reads not ("and" not)*.
func (s *scanner) parseAnd() (*exprNode, error) {
	return s.parseChain("and", exprAnd, s.parseNot)
}

// parseChain reads next (word next)* into one node of kind, or returns the
// lone operand when the word never appears.
func (s *scanner) parseChain(word string, kind exprKind, next func() (*exprNode, error)) (*exprNode, error) {
	first, err := next()
	if err != nil {
		return nil, err
	}
	args := []*exprNode{first}
	for s.acceptWord(word) {
		n, err := next()
		if err != nil {
			return nil, err
		}
		args = append(args, n)
	}
	if len(args) == 1 {
		return first, nil
	}
	return &exprNode{kind: kind, args: args}, nil
}

// parseNot reads "not" not | compare.
func (s *scanner) parseNot() (*exprNode, error) {
	s.skipSpace()
	if s.peekWord() == "not" {
		s.pos += len("not")
		if err := s.enter(); err != nil {
			return nil, err
		}
		defer s.leave()
		operand, err := s.parseNot()
		if err != nil {
			return nil, err
		}
		return &exprNode{kind: exprNot, operand: operand}, nil
	}
	return s.parseCompare()
}

// parseCompare reads concat (op concat)*, Python's chained comparison:
// "a < b < c" is "a < b and b < c".
func (s *scanner) parseCompare() (*exprNode, error) {
	first, err := s.parseConcat()
	if err != nil {
		return nil, err
	}
	node := &exprNode{kind: exprCompare, args: []*exprNode{first}}
	for {
		op := s.compareOp()
		if op == "" {
			break
		}
		n, err := s.parseConcat()
		if err != nil {
			return nil, err
		}
		node.ops = append(node.ops, op)
		node.args = append(node.args, n)
	}
	if len(node.ops) == 0 {
		return first, nil
	}
	return node, nil
}

// compareOp consumes the comparison operator under the cursor, or returns
// "".
func (s *scanner) compareOp() string {
	s.skipSpace()
	for _, op := range comparisons {
		if strings.HasPrefix(s.src[s.pos:], op) {
			s.pos += len(op)
			return op
		}
	}
	if s.acceptWord("in") {
		return "in"
	}
	// "not in" is one operator; a "not" that is not followed by "in" here
	// is left for the caller to refuse.
	if s.peekWord() == "not" {
		save := s.pos
		s.pos += len("not")
		if s.acceptWord("in") {
			return "not in"
		}
		s.pos = save
	}
	return ""
}

// parseConcat reads unary ("~" unary)*.
func (s *scanner) parseConcat() (*exprNode, error) {
	first, err := s.parseUnary()
	if err != nil {
		return nil, err
	}
	args := []*exprNode{first}
	for s.acceptSymbol("~") {
		n, err := s.parseUnary()
		if err != nil {
			return nil, err
		}
		args = append(args, n)
	}
	if len(args) == 1 {
		return first, nil
	}
	return &exprNode{kind: exprConcat, args: args}, nil
}

// parseUnary reads a negative number, or a primary and its filters and
// tests.
func (s *scanner) parseUnary() (*exprNode, error) {
	s.skipSpace()
	if !s.done() && s.peek() == '-' {
		// A minus is part of a number literal only; anywhere else it is
		// arithmetic, which this grammar leaves out.
		if s.pos+1 < len(s.src) && s.src[s.pos+1] >= '0' && s.src[s.pos+1] <= '9' {
			s.pos++
			lit, err := s.number()
			if err != nil {
				return nil, err
			}
			return s.parsePostfix(negate(lit))
		}
		return nil, fmt.Errorf("%w: arithmetic ('-') is not supported; compute the value in a task", ErrSyntax)
	}
	primary, err := s.parsePrimary()
	if err != nil {
		return nil, err
	}
	return s.parsePostfix(primary)
}

// negate returns a number literal's negative.
func negate(lit *exprNode) *exprNode {
	switch v := lit.value.(type) {
	case int64:
		lit.value = -v
	case float64:
		lit.value = -v
	}
	return lit
}

// parsePostfix reads the filters and tests after an operand.
func (s *scanner) parsePostfix(node *exprNode) (*exprNode, error) {
	chain := 0
	for {
		s.skipSpace()
		var err error
		switch {
		case !s.done() && s.peek() == '|' && !strings.HasPrefix(s.src[s.pos:], "||"):
			s.pos++
			node, err = s.filter(node)
		case s.peekWord() == "is":
			s.pos += len("is")
			node, err = s.test(node)
		default:
			return node, nil
		}
		if err != nil {
			return nil, err
		}
		chain++
		if chain > maxFilterChain {
			return nil, fmt.Errorf("%w: more than %d chained filters and tests", ErrTooLarge, maxFilterChain)
		}
	}
}

// filter reads one filter name and its argument list, with the pipe
// consumed, and checks it against the closed set.
func (s *scanner) filter(operand *exprNode) (*exprNode, error) {
	s.skipSpace()
	name, err := s.ident()
	if err != nil {
		return nil, err
	}
	if !isKnownFilter(name) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownFilter, name)
	}
	node := &exprNode{kind: exprFilter, operand: operand, name: name}
	if s.acceptSymbol("(") {
		if node.args, err = s.arguments(")"); err != nil {
			return nil, err
		}
	}
	if err := checkFilterArgs(name, len(node.args)); err != nil {
		return nil, err
	}
	return node, nil
}

// test reads one test, with "is" consumed: an optional not, the name, and
// its argument, in parentheses or, as Jinja2 allows, bare.
func (s *scanner) test(operand *exprNode) (*exprNode, error) {
	node := &exprNode{kind: exprTest, operand: operand, negate: s.acceptWord("not")}
	s.skipSpace()
	name, err := s.ident()
	if err != nil {
		return nil, err
	}
	spec, ok := tests[name]
	if !ok {
		return nil, fmt.Errorf("%w: unknown test %q", ErrSyntax, name)
	}
	node.name = name
	switch {
	case s.acceptSymbol("("):
		if node.args, err = s.arguments(")"); err != nil {
			return nil, err
		}
	case spec.arity == 1:
		if err := s.enter(); err != nil {
			return nil, err
		}
		arg, err := s.parseUnary()
		s.leave()
		if err != nil {
			return nil, err
		}
		node.args = []*exprNode{arg}
	}
	if len(node.args) != spec.arity {
		return nil, fmt.Errorf("%w: test %q takes %d argument(s), got %d", ErrSyntax, name, spec.arity, len(node.args))
	}
	return node, nil
}

// arguments reads a comma-separated list of expressions up to close, with
// the opening bracket consumed. A trailing comma is allowed, as in Jinja2.
func (s *scanner) arguments(close string) ([]*exprNode, error) {
	if err := s.enter(); err != nil {
		return nil, err
	}
	defer s.leave()
	var args []*exprNode
	for {
		if s.acceptSymbol(close) {
			return args, nil
		}
		arg, err := s.parseOr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if len(args) > maxLiteralItems {
			return nil, fmt.Errorf("%w: more than %d items in one list", ErrTooLarge, maxLiteralItems)
		}
		if s.acceptSymbol(close) {
			return args, nil
		}
		if !s.acceptSymbol(",") {
			if s.done() {
				return nil, fmt.Errorf("%w: unterminated list, expected %q", ErrSyntax, close)
			}
			return nil, fmt.Errorf("%w: unexpected %q in a list, expected , or %s", ErrSyntax, s.peek(), close)
		}
	}
}
