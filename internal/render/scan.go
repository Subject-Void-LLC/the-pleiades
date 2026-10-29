package render

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
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
// The escapes are Python's, which Jinja2 decodes its literals with:
// backslash, both quotes, a b f n r t v, an octal \ooo of one to three
// digits, \xhh, \u with four hex digits and \U with eight, each naming a
// code point, and a backslash before a newline, which joins the lines. An
// unrecognized escape is a syntax error rather than a literal backslash,
// where Python keeps it, because the two readings differ and guessing wrong
// silently changes an injected value. \N{name} is refused for the same
// reason: it needs Unicode's name table.
//
// There is deliberately no length check here. A literal is a substring of
// the source that contains it, and parse already refuses a source past
// maxSourceBytes before any of this runs.
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
			if err := s.escape(&b); err != nil {
				return "", err
			}
		default:
			b.WriteByte(c)
		}
	}
}

// simpleEscapes are the one-character escapes and what each stands for.
var simpleEscapes = map[byte]string{
	'\\': "\\", '\'': "'", '"': "\"", 'a': "\a", 'b': "\b", 'f': "\f",
	'n': "\n", 'r': "\r", 't': "\t", 'v': "\v", '\n': "",
}

// escape decodes one escape sequence, with its backslash consumed.
func (s *scanner) escape(b *strings.Builder) error {
	if s.done() {
		return fmt.Errorf("%w: unterminated escape sequence", ErrSyntax)
	}
	esc := s.peek()
	s.pos++
	if out, ok := simpleEscapes[esc]; ok {
		b.WriteString(out)
		return nil
	}
	digits, base := 0, 16
	switch {
	case esc >= '0' && esc <= '7':
		// Up to three octal digits, the first already read.
		s.pos--
		digits, base = 3, 8
	case esc == 'x':
		digits = 2
	case esc == 'u':
		digits = 4
	case esc == 'U':
		digits = 8
	default:
		return fmt.Errorf("%w: unsupported escape sequence %q", ErrSyntax, "\\"+string(esc))
	}
	start := s.pos
	for s.pos-start < digits && !s.done() && isDigitIn(s.peek(), base) {
		s.pos++
	}
	if base == 16 && s.pos-start != digits {
		return fmt.Errorf("%w: escape \\%c needs %d hex digits", ErrSyntax, esc, digits)
	}
	code, err := strconv.ParseUint(s.src[start:s.pos], base, 32)
	if err != nil || code > unicode.MaxRune || (code >= 0xD800 && code <= 0xDFFF) {
		return fmt.Errorf("%w: escape %q is not a character", ErrSyntax, s.src[start-1:s.pos])
	}
	b.WriteRune(rune(code))
	return nil
}

// isDigitIn reports whether c is a digit in base 8 or 16.
func isDigitIn(c byte, base int) bool {
	if base == 8 {
		return c >= '0' && c <= '7'
	}
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// isIdentStart reports whether c may begin an identifier.
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart reports whether c may continue an identifier.
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
