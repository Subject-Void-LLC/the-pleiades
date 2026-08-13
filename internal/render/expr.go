package render

import (
	"fmt"
	"strconv"
	"strings"
)

// The expression grammar, in the order the parser reads it:
//
//	expr    := path ("|" filter)*
//	path    := ident (("." ident) | ("[" string "]") | ("[" integer "]"))*
//	filter  := ident ("(" literal ("," literal)* ")")?
//	literal := string | integer
//
// Literals appear only as filter arguments. There is no arithmetic, no
// comparison, no function call, and no way to name anything the caller did
// not put in the variable map. That is the whole language.

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

// expression is one compiled {{ ... }} action.
type expression struct {
	// source is the original text between the delimiters, kept so an error
	// can quote what the author wrote rather than a reconstruction of it.
	source string

	// root is the top-level variable name this expression reads. It is
	// what Template.Names reports.
	root string

	// steps descend from root, in order.
	steps []step

	// filters apply left to right to whatever the path resolved to.
	filters []filterCall
}

// filterCall is one filter and its literal arguments.
type filterCall struct {
	name string
	args []any
}

// reference renders the path back into the spelling an author would
// recognize, for error messages. It is built from the parsed form rather
// than sliced out of the source so that it stays correct regardless of the
// whitespace the author used.
func (e *expression) reference() string {
	var b strings.Builder
	b.WriteString(e.root)
	for _, s := range e.steps {
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

// scanner is a cursor over one expression's text.
type scanner struct {
	src string
	pos int
}

// parseExpression compiles the text between one pair of action delimiters.
func parseExpression(body string) (*expression, error) {
	s := &scanner{src: body}
	expr := &expression{source: strings.TrimSpace(body)}

	s.skipSpace()
	root, err := s.ident()
	if err != nil {
		return nil, expr.wrap(err)
	}
	expr.root = root

	steps, err := s.path()
	if err != nil {
		return nil, expr.wrap(err)
	}
	expr.steps = steps

	for {
		s.skipSpace()
		if s.done() {
			return expr, nil
		}
		if s.peek() != '|' {
			return nil, expr.wrap(fmt.Errorf("%w: unexpected %q", ErrSyntax, s.peek()))
		}
		s.pos++

		f, err := s.filter()
		if err != nil {
			return nil, expr.wrap(err)
		}
		if !isKnownFilter(f.name) {
			return nil, expr.wrap(fmt.Errorf("%w: %q", ErrUnknownFilter, f.name))
		}
		if err := checkFilterArgs(f); err != nil {
			return nil, expr.wrap(err)
		}

		expr.filters = append(expr.filters, f)
		if len(expr.filters) > maxFilterChain {
			return nil, expr.wrap(fmt.Errorf("%w: more than %d chained filters", ErrTooLarge, maxFilterChain))
		}
	}
}

// wrap adds the offending expression's own text to an error while leaving
// the sentinel underneath reachable by errors.Is.
//
// Every helper below returns an error that already wraps the right
// sentinel, so this must not introduce one of its own. An earlier version
// wrapped everything in ErrSyntax here, which turned a path-depth refusal
// (a size limit) into a syntax error and made ErrTooLarge unreachable for
// the two limits enforced during parsing.
func (e *expression) wrap(err error) error {
	return fmt.Errorf("%w (in %q)", err, e.source)
}

// path reads the descent steps that follow a root identifier.
func (s *scanner) path() ([]step, error) {
	var steps []step

	for {
		if s.done() {
			return steps, nil
		}

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
}

// subscript reads one bracketed key or index, with the opening bracket
// already consumed.
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
			return st, err
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

// filter reads one filter name and its optional argument list, with the
// pipe already consumed.
func (s *scanner) filter() (filterCall, error) {
	s.skipSpace()

	name, err := s.ident()
	if err != nil {
		return filterCall{}, err
	}
	f := filterCall{name: name}

	s.skipSpace()
	if s.done() || s.peek() != '(' {
		return f, nil
	}
	s.pos++

	// An empty argument list, "default()", is written as an explicit empty
	// pair and is legal: the filter's own arity check decides whether it
	// is acceptable, not the parser.
	s.skipSpace()
	if !s.done() && s.peek() == ')' {
		s.pos++
		return f, nil
	}

	for {
		s.skipSpace()
		arg, err := s.literal()
		if err != nil {
			return filterCall{}, err
		}
		f.args = append(f.args, arg)

		s.skipSpace()
		if s.done() {
			return filterCall{}, fmt.Errorf("%w: unterminated argument list for filter %q", ErrSyntax, name)
		}
		switch s.peek() {
		case ',':
			s.pos++
		case ')':
			s.pos++
			return f, nil
		default:
			return filterCall{}, fmt.Errorf("%w: unexpected %q in the argument list for filter %q", ErrSyntax, s.peek(), name)
		}
	}
}

// literal reads one string or integer filter argument.
func (s *scanner) literal() (any, error) {
	if s.done() {
		return nil, fmt.Errorf("%w: expected a value", ErrSyntax)
	}
	if s.peek() == '\'' || s.peek() == '"' {
		return s.quoted()
	}
	return s.integer()
}
