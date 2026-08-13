package render

import (
	"fmt"
	"strconv"
	"strings"
)

// The lowest layer of the expression parser: byte-level cursor primitives.
//
// Everything here works on bytes rather than runes, which is safe because
// every token this grammar recognizes is ASCII. A multi-byte rune can only
// appear inside a quoted string literal, where it is copied through
// untouched, or outside any token, where it is a syntax error. That keeps
// the fuzz target free of the invalid-UTF-8 special cases a rune-based
// scanner would need.

// done reports whether the cursor has consumed the whole expression.
func (s *scanner) done() bool { return s.pos >= len(s.src) }

// peek returns the byte under the cursor. Callers check done first.
func (s *scanner) peek() byte { return s.src[s.pos] }

// skipSpace advances past insignificant whitespace. Newlines are included
// because a multi-line injector template may wrap a long expression.
func (s *scanner) skipSpace() {
	for !s.done() {
		switch s.peek() {
		case ' ', '\t', '\r', '\n':
			s.pos++
		default:
			return
		}
	}
}

// ident reads one identifier: a leading letter or underscore, then letters,
// digits, and underscores.
//
// This is deliberately narrower than Jinja2, which allows any Python
// identifier including non-ASCII ones. Narrower is right here: an
// identifier in this grammar names a credential type input id, and that id
// also becomes part of an environment variable name, so it was already
// constrained to this character set one layer up.
func (s *scanner) ident() (string, error) {
	start := s.pos
	if s.done() || !isIdentStart(s.peek()) {
		if s.done() {
			return "", fmt.Errorf("%w: expected a name", ErrSyntax)
		}
		return "", fmt.Errorf("%w: expected a name, found %q", ErrSyntax, s.peek())
	}
	s.pos++

	for !s.done() && isIdentPart(s.peek()) {
		s.pos++
	}
	return s.src[start:s.pos], nil
}

// integer reads an optionally negative base-10 integer.
func (s *scanner) integer() (int, error) {
	start := s.pos
	if !s.done() && s.peek() == '-' {
		s.pos++
	}

	digits := s.pos
	for !s.done() && s.peek() >= '0' && s.peek() <= '9' {
		s.pos++
	}
	if s.pos == digits {
		if s.done() {
			return 0, fmt.Errorf("%w: expected a number", ErrSyntax)
		}
		return 0, fmt.Errorf("%w: expected a number, found %q", ErrSyntax, s.peek())
	}

	n, err := strconv.Atoi(s.src[start:s.pos])
	if err != nil {
		// Reachable only for a value outside the int range, which Atoi
		// reports and the digit loop above cannot.
		return 0, fmt.Errorf("%w: number %q is out of range", ErrSyntax, s.src[start:s.pos])
	}
	return n, nil
}

// quoted reads a single- or double-quoted string literal.
//
// The escape set is closed: \\ \' \" \n \t. An unrecognized escape is a
// syntax error rather than a literal backslash, because the two readings
// differ and guessing wrong silently changes an injected value.
//
// There is deliberately no length check here. A literal is a substring of
// the source that contains it, and parse already refuses a source past
// maxSourceBytes before any of this runs, so a per-literal bound could
// never fire. An earlier version had one, and its only real effect was to
// suggest a protection that was not doing anything.
func (s *scanner) quoted() (string, error) {
	quote := s.peek()
	s.pos++

	var b strings.Builder
	for {
		if s.done() {
			return "", fmt.Errorf("%w: unterminated string literal", ErrSyntax)
		}

		c := s.peek()
		s.pos++

		switch c {
		case quote:
			return b.String(), nil

		case '\\':
			if s.done() {
				return "", fmt.Errorf("%w: unterminated escape sequence", ErrSyntax)
			}
			esc := s.peek()
			s.pos++
			switch esc {
			case '\\':
				b.WriteByte('\\')
			case '\'':
				b.WriteByte('\'')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", fmt.Errorf("%w: unsupported escape sequence %q", ErrSyntax, "\\"+string(esc))
			}

		default:
			b.WriteByte(c)
		}
	}
}

// isIdentStart reports whether c may begin an identifier.
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart reports whether c may continue an identifier.
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
