// Package sqllint reads PostgreSQL source the way the server's own lexer
// does, far enough to split it into statements and to say what each
// statement is: whether it removes or rewrites something (the
// additive-only rule), whether it ends the transaction it runs in, and
// whether it cannot run inside one. It is not a parser. It never decides
// whether SQL is valid; the server does that when the SQL runs.
//
// What it does get right is what a pattern match gets wrong: a semicolon
// or a DROP inside a string, a quoted identifier, a comment (nested, as
// PostgreSQL nests them) or a dollar-quoted function body is not a
// statement boundary or a keyword.
package sqllint

import (
	"fmt"
	"strings"
)

// Kind is what a token is.
type Kind int

// The kinds of token.
const (
	// Word is a bare keyword or identifier, held upper-cased.
	Word Kind = iota
	// QuotedIdent is a "double-quoted" identifier, held as written.
	QuotedIdent
	// String is a string constant of any form ('', E'', U&'', B'', X'',
	// or dollar quoted), held as written.
	String
	// Other is a number, an operator, a parameter or punctuation.
	Other
)

// Token is one token of a statement.
type Token struct {
	Kind Kind
	Text string
	Line int
}

// Statement is one statement: its tokens, the line it starts on, and its
// source text.
type Statement struct {
	Line   int
	Text   string
	Tokens []Token
}

// Words is the statement's bare words, upper-cased, in order.
func (s Statement) Words() []string {
	var out []string
	for _, t := range s.Tokens {
		if t.Kind == Word {
			out = append(out, t.Text)
		}
	}
	return out
}

// asciiSpace is the white space the lexer skips.
const asciiSpace = " \t\r\n\f\v"

// MaxSourceBytes bounds the source Split reads.
const MaxSourceBytes = 8 << 20

// Split reads src into its statements, split at each semicolon outside a
// string, identifier, comment or dollar quote. A statement holding only
// comments is not one. It refuses source it cannot read to the end: an
// unterminated string, identifier, comment or dollar quote, a backslash
// at the start of a line (a psql command, which is not SQL), and source
// past MaxSourceBytes.
func Split(src string) ([]Statement, error) {
	stmts, _, err := scan(src)
	return stmts, err
}

// comment is one -- comment: its line, its text from the dashes on, and
// whether a token came before it.
type comment struct {
	line       int
	text       string
	afterToken bool
}

// scan is Split, also returning every -- comment.
func scan(src string) ([]Statement, []comment, error) {
	if len(src) > MaxSourceBytes {
		return nil, nil, fmt.Errorf("the SQL is over %d bytes", MaxSourceBytes)
	}
	l := lexer{src: src, line: 1}
	var out []Statement
	var cur []Token
	start := -1
	flush := func(end int) {
		if len(cur) > 0 {
			// Only the white space skipSpaceAndComments skips is trimmed:
			// TrimSpace would also trim U+0085 and U+00A0, which the
			// server, like this lexer, reads as identifier bytes.
			out = append(out, Statement{Line: cur[0].Line, Text: strings.TrimRight(src[start:end], asciiSpace), Tokens: cur})
		}
		cur, start = nil, -1
	}
	for {
		l.skipSpaceAndComments()
		if l.err != nil {
			return nil, nil, l.err
		}
		if l.pos >= len(src) {
			flush(len(src))
			return out, l.comments, nil
		}
		if src[l.pos] == ';' {
			flush(l.pos)
			l.pos++
			continue
		}
		at := l.pos
		tok, ok := l.next()
		if !ok {
			return nil, nil, l.err
		}
		l.seenToken = true
		if start < 0 {
			start = at
		}
		cur = append(cur, tok)
	}
}

// lexer walks src.
type lexer struct {
	src       string
	pos       int
	line      int
	err       error
	seenToken bool
	comments  []comment
}

// fail records the first error, at the current line.
func (l *lexer) fail(format string, args ...any) {
	if l.err == nil {
		l.err = fmt.Errorf("line %d: "+format, append([]any{l.line}, args...)...)
	}
}

// advance moves past n bytes, counting lines.
func (l *lexer) advance(n int) {
	l.line += strings.Count(l.src[l.pos:l.pos+n], "\n")
	l.pos += n
}

// atLineStart reports whether only spaces and tabs precede pos on its line.
func (l *lexer) atLineStart() bool {
	for i := l.pos - 1; i >= 0; i-- {
		switch l.src[i] {
		case '\n':
			return true
		case ' ', '\t', '\r':
			continue
		default:
			return false
		}
	}
	return true
}

// skipSpaceAndComments moves past white space, -- comments and nested
// /* */ comments.
func (l *lexer) skipSpaceAndComments() {
	for l.pos < len(l.src) && l.err == nil {
		c := l.src[l.pos]
		switch {
		case strings.IndexByte(asciiSpace, c) >= 0:
			l.advance(1)
		case strings.HasPrefix(l.src[l.pos:], "--"):
			end := strings.IndexByte(l.src[l.pos:], '\n')
			if end < 0 {
				end = len(l.src) - l.pos
			}
			l.comments = append(l.comments, comment{line: l.line, text: l.src[l.pos : l.pos+end], afterToken: l.seenToken})
			l.advance(end)
		case strings.HasPrefix(l.src[l.pos:], "/*"):
			l.blockComment()
		default:
			return
		}
	}
}

// blockComment moves past a /* */ comment, which nests in PostgreSQL.
func (l *lexer) blockComment() {
	startLine := l.line
	depth := 0
	for l.pos < len(l.src) {
		switch {
		case strings.HasPrefix(l.src[l.pos:], "/*"):
			depth++
			l.advance(2)
		case strings.HasPrefix(l.src[l.pos:], "*/"):
			depth--
			l.advance(2)
			if depth == 0 {
				return
			}
		default:
			l.advance(1)
		}
	}
	l.line = startLine
	l.fail("a /* comment is never closed")
}

// next reads one token at pos.
func (l *lexer) next() (Token, bool) {
	line := l.line
	rest := l.src[l.pos:]
	c := rest[0]
	switch {
	case c == '\\' && l.atLineStart():
		l.fail("a line starting with a backslash is a psql command, not SQL")
		return Token{}, false
	case c == '\'':
		return l.quoted(line, 0, '\'', false)
	case (c == 'E' || c == 'e') && len(rest) > 1 && rest[1] == '\'':
		return l.quoted(line, 1, '\'', true)
	case (c == 'B' || c == 'b' || c == 'X' || c == 'x' || c == 'N' || c == 'n') && len(rest) > 1 && rest[1] == '\'':
		return l.quoted(line, 1, '\'', false)
	case (c == 'U' || c == 'u') && len(rest) > 2 && rest[1] == '&' && rest[2] == '\'':
		return l.quoted(line, 2, '\'', false)
	case (c == 'U' || c == 'u') && len(rest) > 2 && rest[1] == '&' && rest[2] == '"':
		tok, ok := l.quoted(line, 2, '"', false)
		tok.Kind = QuotedIdent
		return tok, ok
	case c == '"':
		tok, ok := l.quoted(line, 0, '"', false)
		tok.Kind = QuotedIdent
		return tok, ok
	case c == '$':
		if tag, ok := dollarTag(rest); ok {
			return l.dollarQuoted(line, tag)
		}
		n := 1
		for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
			n++
		}
		l.advance(n)
		return Token{Kind: Other, Text: rest[:n], Line: line}, true
	case isWordStart(c):
		n := 1
		for n < len(rest) && isWordPart(rest[n]) {
			n++
		}
		l.advance(n)
		return Token{Kind: Word, Text: strings.ToUpper(rest[:n]), Line: line}, true
	default:
		l.advance(1)
		return Token{Kind: Other, Text: rest[:1], Line: line}, true
	}
}

// quoted reads a string or identifier whose quote is at pos+prefix. A
// doubled quote is the quote itself; in an escape string (E”) a
// backslash also escapes the byte after it.
func (l *lexer) quoted(line, prefix int, quote byte, backslash bool) (Token, bool) {
	rest := l.src[l.pos:]
	i := prefix + 1
	for i < len(rest) {
		switch {
		case backslash && rest[i] == '\\' && i+1 < len(rest):
			i += 2
		case rest[i] == quote && i+1 < len(rest) && rest[i+1] == quote:
			i += 2
		case rest[i] == quote:
			l.advance(i + 1)
			return Token{Kind: String, Text: rest[:i+1], Line: line}, true
		default:
			i++
		}
	}
	what := "string"
	if quote == '"' {
		what = "quoted identifier"
	}
	l.fail("a %s is never closed", what)
	return Token{}, false
}

// dollarTag returns the $tag$ (or $$) opening a dollar quote at the start
// of s, if one does.
func dollarTag(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	if s[1] == '$' {
		return "$$", true
	}
	if !isWordStart(s[1]) {
		return "", false
	}
	for i := 2; i < len(s); i++ {
		switch {
		case s[i] == '$':
			return s[:i+1], true
		case !isTagPart(s[i]):
			return "", false
		}
	}
	return "", false
}

// dollarQuoted reads a dollar-quoted string to its closing tag.
func (l *lexer) dollarQuoted(line int, tag string) (Token, bool) {
	rest := l.src[l.pos:]
	end := strings.Index(rest[len(tag):], tag)
	if end < 0 {
		l.fail("a %s quote is never closed", tag)
		return Token{}, false
	}
	n := len(tag) + end + len(tag)
	l.advance(n)
	return Token{Kind: String, Text: rest[:n], Line: line}, true
}

// isWordStart reports whether c begins a bare word.
func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// isWordPart reports whether c continues a bare word.
func isWordPart(c byte) bool { return isWordStart(c) || (c >= '0' && c <= '9') || c == '$' }

// isTagPart reports whether c continues a dollar quote's tag, which,
// unlike a word, cannot hold a dollar sign.
func isTagPart(c byte) bool { return isWordStart(c) || (c >= '0' && c <= '9') }
