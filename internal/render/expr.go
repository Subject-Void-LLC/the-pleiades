package render

import (
	"fmt"
	"strconv"
	"strings"
)

// The expression grammar, in Jinja2's precedence, loosest first:
//
//	expr    := and ("or" and)*
//	and     := not ("and" not)*
//	not     := "not" not | compare
//	compare := concat (cmpop concat)*        cmpop: == != < <= > >= in, not in
//	concat  := unary ("~" unary)*
//	unary   := "-" number | primary postfix*
//	postfix := "|" filter | "is" ["not"] test [arg]
//	primary := literal | path [".items()" | ".keys()" | ".values()"]
//	         | "(" expr ")" | "(" expr "," ... ")" | "[" ... "]" | "{" string ":" expr ... "}"
//	path    := ident (("." ident) | ("[" string "]") | ("[" integer "]"))*
//	literal := string | integer | float | true | false | none
//
// As in Jinja2, a filter or a test binds tighter than every operator, so
// "a ~ b | upper" uppercases b alone, and and/or return one of their
// operands rather than a boolean, short-circuiting so a branch not taken
// reads nothing. There is no arithmetic (Jinja2's + - * / // % **): "*"
// and "**" alone turn a short template into an unbounded amount of text or
// a number of unbounded size, and a template that computes is better a
// task. A path's subscripts are literal, so every path is known at compile
// time, which is what lets a caller check what a template reads.

// stepKind distinguishes the two ways a path descends into a value.
type stepKind int

const (
	// stepKey reads a named key, whether written as ".name" or as
	// "['name']". Both spellings mean the same thing, so they compile to
	// the same step rather than to two cases the evaluator must handle.
	stepKey stepKind = iota

	// stepIndex reads a positional element, written as "[0]".
	stepIndex
)

// step is one descent in a path.
type step struct {
	kind  stepKind
	key   string
	index int
}

// exprKind says what one node of an expression tree is.
type exprKind int

const (
	exprRef     exprKind = iota // a path: root and steps
	exprLiteral                 // a constant: value
	exprList                    // a list or tuple literal: args
	exprMap                     // a mapping literal: keys and args
	exprMethod                  // .items(), .keys() or .values() on operand
	exprNot                     // not operand
	exprAnd                     // args joined by and
	exprOr                      // args joined by or
	exprCompare                 // args[0] ops[0] args[1] ops[1] args[2] ...
	exprConcat                  // args joined by ~
	exprFilter                  // operand | name(args)
	exprTest                    // operand is [not] name args
)

// exprNode is one node of an expression tree. kind decides which fields
// are meaningful.
type exprNode struct {
	kind exprKind

	// root and steps are an exprRef's path.
	root  string
	steps []step

	// value is an exprLiteral's constant: a string, int64, float64, bool
	// or nil.
	value any

	// keys are an exprMap's keys, one per args entry.
	keys []string

	// args are a list's items, a mapping's values, the operands of and,
	// or, a comparison and ~, and a filter's or a test's arguments.
	args []*exprNode

	// ops are an exprCompare's operators, one between each two args.
	ops []string

	// operand is what a method, not, filter or test applies to.
	operand *exprNode

	// name is a method's, filter's or test's name.
	name string

	// negate is "is not".
	negate bool
}

// expression is one compiled expression: the text of an action or of a
// statement's expression, and its tree.
type expression struct {
	// source is the original text, kept so an error can quote what the
	// author wrote rather than a reconstruction of it.
	source string

	// body is the tree.
	body *exprNode
}

// reference renders a path back into the spelling an author would
// recognize, for error messages. It is built from the parsed form rather
// than sliced out of the source so that it stays correct regardless of the
// whitespace the author used.
func (n *exprNode) reference() string {
	var b strings.Builder
	b.WriteString(n.root)
	for _, s := range n.steps {
		switch s.kind {
		case stepKey:
			b.WriteString(".")
			b.WriteString(s.key)
		case stepIndex:
			b.WriteString("[")
			b.WriteString(strconv.Itoa(s.index))
			b.WriteString("]")
		}
	}
	return b.String()
}

// path is an exprRef's reference as ExpressionInfo spells it: root, then
// each key or index.
func (n *exprNode) path() []string {
	path := []string{n.root}
	for _, s := range n.steps {
		if s.kind == stepIndex {
			path = append(path, strconv.Itoa(s.index))
		} else {
			path = append(path, s.key)
		}
	}
	return path
}

// scanner is a cursor over one expression's text.
type scanner struct {
	src string
	pos int

	// depth counts nested sub-expressions (parentheses, literals, filter
	// and test arguments, nots) toward maxExprDepth.
	depth int
}

// parseExpression compiles the text of one expression.
func parseExpression(body string) (*expression, error) {
	s := &scanner{src: body}
	expr := &expression{source: strings.TrimSpace(body)}

	s.skipSpace()
	node, err := s.parseOr()
	if err != nil {
		return nil, expr.wrap(err)
	}
	s.skipSpace()
	if !s.done() {
		return nil, expr.wrap(s.unexpected())
	}
	expr.body = node
	return expr, nil
}

// unexpected describes what stopped the parser, naming the unsupported
// Jinja2 forms an author is likely to have meant.
func (s *scanner) unexpected() error {
	c := s.peek()
	switch {
	case strings.IndexByte("+-*/%", c) >= 0:
		return fmt.Errorf("%w: arithmetic (%q) is not supported; compute the value in a task", ErrSyntax, c)
	case s.peekWord() == "if":
		return fmt.Errorf("%w: an inline if expression is not supported; use an if statement", ErrSyntax)
	}
	return fmt.Errorf("%w: unexpected %q", ErrSyntax, c)
}

// wrap adds the offending expression's own text to an error while leaving
// the sentinel underneath reachable by errors.Is.
//
// Every helper returns an error that already wraps the right sentinel, so
// this must not introduce one of its own. An earlier version wrapped
// everything in ErrSyntax here, which turned a path-depth refusal (a size
// limit) into a syntax error and made ErrTooLarge unreachable for the
// limits enforced during parsing.
func (e *expression) wrap(err error) error {
	return fmt.Errorf("%w (in %q)", err, e.source)
}

// enter counts one level of nesting and refuses past maxExprDepth.
func (s *scanner) enter() error {
	s.depth++
	if s.depth > maxExprDepth {
		return fmt.Errorf("%w: an expression nested deeper than %d", ErrTooLarge, maxExprDepth)
	}
	return nil
}

// leave ends one level of nesting.
func (s *scanner) leave() { s.depth-- }
