// Package render: reading the {% %} statements a template may hold
// (if, for, set) and refusing every other statement by name.
package render

import (
	"fmt"
	"slices"
	"strings"
)

// The statement grammar, Jinja2's own spelling and meaning for each:
//
//	{% if expr %} ... {% elif expr %} ... {% else %} ... {% endif %}
//	{% for x in expr %} ... {% else %} ... {% endfor %}
//	{% for k, v in expr %} ... {% endfor %}   (each item a pair, as .items() gives)
//	{% set name = expr %}
//	{% raw %} ... {% endraw %}
//
// expr is expr.go's expression. A condition is its value's truth, the way
// Jinja2 tests one: false, none, zero, and an empty string, list or mapping
// are false, and everything else is true. A for loop over a mapping walks
// its keys in sorted order, since a Go map keeps no order and Jinja2's
// insertion order cannot be recovered from decoded YAML or JSON. Every
// other statement is refused by name.

// Statement keywords.
const (
	stmtIf     = "if"
	stmtElif   = "elif"
	stmtElse   = "else"
	stmtEndIf  = "endif"
	stmtFor    = "for"
	stmtEndFor = "endfor"
	stmtSet    = "set"
	stmtRaw    = "raw"
	stmtEndRaw = "endraw"

	// keywordIn separates a for loop's names from its list.
	keywordIn = "in"

	// loopName is the variable a for loop binds to its counters.
	loopName = "loop"
)

// unsupportedStatements are Jinja2 statements, and those of the extensions
// Ansible enables, that this renderer refuses. Each is named in the error,
// so an author learns which statement to rewrite rather than that the
// template "is not valid".
var unsupportedStatements = map[string]bool{
	"macro": true, "endmacro": true, "call": true, "endcall": true,
	"filter": true, "endfilter": true, "include": true, "import": true,
	"from": true, "extends": true, "block": true, "endblock": true,
	"with": true, "endwith": true, "autoescape": true, "endautoescape": true,
	"endset": true, "do": true, "break": true, "continue": true,
	"trans": true, "pluralize": true, "endtrans": true,
}

// statement parses one statement's body, with its delimiters and trim
// markers removed, and applies it to the tree. raw is handled by the
// caller, since it consumes the text after its tag.
func (b *builder) statement(body string) error {
	s := &scanner{src: body}
	s.skipSpace()
	if s.done() {
		return fmt.Errorf("%w: an empty %s %s", ErrSyntax, blockOpen, blockClose)
	}
	keyword, err := s.ident()
	if err != nil {
		return statementError(body, err)
	}
	if err := b.apply(keyword, s.src[s.pos:]); err != nil {
		return statementError(body, err)
	}
	return nil
}

// apply carries out one statement given its keyword and the text after it.
func (b *builder) apply(keyword, rest string) error {
	switch keyword {
	case stmtIf, stmtElif:
		cond, err := parseExpression(rest)
		if err != nil {
			return err
		}
		if keyword == stmtIf {
			return b.openIf(cond)
		}
		return b.elif(cond)

	case stmtElse, stmtEndIf, stmtEndFor:
		if strings.TrimSpace(rest) != "" {
			return fmt.Errorf("%w: %s takes nothing after it", ErrSyntax, keyword)
		}
		switch keyword {
		case stmtElse:
			return b.otherwise()
		case stmtEndIf:
			return b.end(keyword, nodeIf)
		}
		return b.end(keyword, nodeFor)

	case stmtFor:
		loop, err := parseFor(rest)
		if err != nil {
			return err
		}
		return b.openFor(loop)

	case stmtSet:
		name, expr, err := parseSet(rest)
		if err != nil {
			return err
		}
		b.add(node{kind: nodeSet, name: name, expr: expr})
		return nil

	case stmtEndRaw:
		return fmt.Errorf("%w: endraw without a raw", ErrSyntax)
	}

	if unsupportedStatements[keyword] {
		return fmt.Errorf("%w: the %s statement is not supported; this renderer has if, for, set and raw", ErrSyntax, keyword)
	}
	return fmt.Errorf("%w: unknown statement %q; this renderer has if, for, set and raw", ErrSyntax, keyword)
}

// statementError adds the statement's own text to err, keeping its
// sentinel reachable by errors.Is.
func statementError(body string, err error) error {
	return fmt.Errorf("%w (in %s %s %s)", err, blockOpen, strings.TrimSpace(body), blockClose)
}

// parseFor reads what follows for: one or two names, in, and the list.
func parseFor(text string) (*forLoop, error) {
	s := &scanner{src: text}
	loop := &forLoop{}
	for {
		s.skipSpace()
		name, err := s.ident()
		if err != nil {
			return nil, err
		}
		switch {
		case name == loopName:
			return nil, fmt.Errorf("%w: loop is the loop's own variable and cannot be a for loop's name", ErrSyntax)
		case reserved(name):
			return nil, fmt.Errorf("%w: %q is a keyword and cannot be a for loop's name", ErrSyntax, name)
		case slices.Contains(loop.targets, name):
			return nil, fmt.Errorf("%w: a for loop names %q twice", ErrSyntax, name)
		}
		loop.targets = append(loop.targets, name)
		s.skipSpace()
		if s.done() || s.peek() != ',' {
			break
		}
		if len(loop.targets) == 2 {
			return nil, fmt.Errorf("%w: a for loop takes one name or two, not more", ErrSyntax)
		}
		s.pos++
	}
	if in, err := s.ident(); err != nil || in != keywordIn {
		return nil, fmt.Errorf("%w: expected %s after a for loop's names", ErrSyntax, keywordIn)
	}
	iter := s.src[s.pos:]
	expr, err := parseExpression(iter)
	if err != nil {
		if words := strings.Fields(iter); slices.Contains(words, "recursive") {
			return nil, fmt.Errorf("%w: a recursive for loop is not supported", ErrSyntax)
		}
		if words := strings.Fields(iter); slices.Contains(words, stmtIf) {
			return nil, fmt.Errorf("%w: a for loop's inline if is not supported; put an if inside the loop", ErrSyntax)
		}
		return nil, err
	}
	loop.iter = expr
	return loop, nil
}

// reserved reports whether name is a keyword or a constant, which a
// statement cannot bind.
func reserved(name string) bool {
	_, constant := constants[name]
	return constant || keywords[name]
}

// parseSet reads what follows set: one name, =, and the value.
func parseSet(text string) (string, *expression, error) {
	s := &scanner{src: text}
	s.skipSpace()
	name, err := s.ident()
	if err != nil {
		return "", nil, err
	}
	if reserved(name) {
		return "", nil, fmt.Errorf("%w: %q is a keyword and cannot be set", ErrSyntax, name)
	}
	s.skipSpace()
	switch {
	case s.done():
		return "", nil, fmt.Errorf("%w: a set block, {%% set %s %%} ... {%% endset %%}, is not supported; write {%% set %s = value %%}", ErrSyntax, name, name)
	case s.peek() == ',':
		return "", nil, fmt.Errorf("%w: set takes one name, not several", ErrSyntax)
	case s.peek() == '.':
		return "", nil, fmt.Errorf("%w: set takes a name, not an attribute of one", ErrSyntax)
	case s.peek() != '=' || strings.HasPrefix(s.src[s.pos:], "=="):
		return "", nil, fmt.Errorf("%w: expected = after set %s", ErrSyntax, name)
	}
	expr, err := parseExpression(s.src[s.pos+1:])
	if err != nil {
		return "", nil, err
	}
	return name, expr, nil
}
