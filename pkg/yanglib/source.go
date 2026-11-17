// Package yanglib: holding a fetched answer to being the YANG source it was
// asked for, before a client keeps it. A server can answer a schema request
// with something else (an HTML login page, a JSON error, another revision of
// the module), and a file saved under the asked name@revision then misleads
// whoever reads it. So the answer is read as YANG (RFC 7950 section 6's
// tokens: unquoted and quoted strings, statements, comments) far enough to
// say that its one top-level statement is the module or submodule asked for
// and that, when a revision was asked for, it is one of its revision
// statements. Nothing else of it is judged: the source is kept byte for byte
// as the server sent it.
package yanglib

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxSourceDepth bounds how deeply statements may nest in a source this reads,
// far past any real module's, so a hostile answer of nothing but "a {" cannot
// make the reader count without end.
const maxSourceDepth = 1024

// The kinds of token a source holds.
const (
	tokEOF = iota
	tokWord
	tokString
	tokOpen
	tokClose
	tokSemi
)

// CheckSource returns an error unless src is the YANG source of s: valid
// UTF-8 holding no control character but white space, whose one top-level
// statement is "module" (or "submodule", when submodule is true) naming s.Name,
// with s.Revision among its revision statements when s names one. The error
// names what is wrong and the kind of token it met (a word, a quoted string,
// a brace), never any of the answer's text: on RESTCONF the answer reaches
// here before any masking, and a page echoing a login would put the
// credential in the error.
func CheckSource(src []byte, s Schema, submodule bool) error {
	what := "module"
	if submodule {
		what = "submodule"
	}
	fail := func(format string, args ...any) error {
		return fmt.Errorf("the answer is not the YANG source of %s %s: %s", what, s.Key(), fmt.Sprintf(format, args...))
	}
	if !utf8.Valid(src) {
		return fail("it is not UTF-8 text")
	}
	text := strings.TrimPrefix(string(src), "\xef\xbb\xbf")
	for _, r := range text {
		// RFC 7950 section 6.1's characters exclude every control character
		// but the white space it names.
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return fail("it holds a control character")
		}
	}
	revisions, err := readHeader(&lexer{src: text}, what, s.Name)
	if err != nil {
		return fail("%s", err)
	}
	if s.Revision == "" {
		return nil
	}
	for _, r := range revisions {
		if r == s.Revision {
			return nil
		}
	}
	return fail("it has no revision %s statement", s.Revision)
}

// readHeader reads every statement of l, requiring the one top-level
// statement to be keyword naming name, and returns the arguments of that
// statement's own revision substatements.
func readHeader(l *lexer, keyword, name string) ([]string, error) {
	var revisions []string
	depth, closed := 0, false
	for {
		kind, tok, err := l.next()
		if err != nil {
			return nil, err
		}
		switch {
		case kind == tokEOF:
			if !closed {
				return nil, errors.New("it ends before its module statement does")
			}
			return revisions, nil
		case closed:
			return nil, errors.New("it holds more after its module statement")
		case kind == tokClose:
			if depth == 0 {
				return nil, errors.New("it closes a statement it never opened")
			}
			depth--
			closed = depth == 0
			continue
		case kind != tokWord:
			return nil, fmt.Errorf("a statement begins with %s, not a keyword", describe(kind))
		}
		// A statement: its keyword (tok), an optional argument, then ";" or
		// "{" (RFC 7950 section 6.3).
		stmt := tok
		kind, tok, err = l.next()
		if err != nil {
			return nil, err
		}
		arg, hasArg := "", false
		if kind == tokWord || kind == tokString {
			arg, hasArg = tok, true
			if kind, tok, err = l.next(); err != nil {
				return nil, err
			}
		}
		switch {
		case depth == 0 && (stmt != keyword || !hasArg || arg != name):
			// What the statement does say is device text, which on RESTCONF
			// reached here unmasked (a debug page echoing a login), so only
			// what was asked for is named.
			return nil, fmt.Errorf("its first statement is not %s %s", keyword, name)
		case depth == 1 && stmt == "revision" && hasArg:
			revisions = append(revisions, arg)
		}
		switch kind {
		case tokSemi:
			if depth == 0 {
				return nil, fmt.Errorf("its %s statement has no body", keyword)
			}
		case tokOpen:
			if depth++; depth > maxSourceDepth {
				return nil, fmt.Errorf("its statements nest more than %d deep", maxSourceDepth)
			}
		default:
			return nil, fmt.Errorf("a statement ends with %s, not ; or {", describe(kind))
		}
	}
}

// describe names a token's kind for a refusal, never its text, which is the
// device's.
func describe(kind int) string {
	switch kind {
	case tokEOF:
		return "the end of the text"
	case tokOpen:
		return "{"
	case tokClose:
		return "}"
	case tokSemi:
		return ";"
	case tokString:
		return "a quoted string"
	}
	return "a word"
}

// lexer reads RFC 7950 section 6's tokens from src.
type lexer struct {
	src string
	pos int
}

// next returns the next token, skipping white space and comments. A quoted
// string's value is its text with a double-quoted string's escapes read and
// any "+" concatenation joined; line folding, which matters to no keyword or
// identifier, is not undone.
func (l *lexer) next() (int, string, error) {
	if err := l.skip(); err != nil {
		return 0, "", err
	}
	if l.pos >= len(l.src) {
		return tokEOF, "", nil
	}
	switch c := l.src[l.pos]; c {
	case '{':
		l.pos++
		return tokOpen, "{", nil
	case '}':
		l.pos++
		return tokClose, "}", nil
	case ';':
		l.pos++
		return tokSemi, ";", nil
	case '"', '\'':
		value, err := l.quoted()
		return tokString, value, err
	}
	start := l.pos
	for l.pos < len(l.src) && !l.endsWord() {
		l.pos++
	}
	return tokWord, l.src[start:l.pos], nil
}

// endsWord reports whether the byte at pos ends an unquoted string: white
// space, a quote, ";", a brace, or a comment's start.
func (l *lexer) endsWord() bool {
	switch l.src[l.pos] {
	case ' ', '\t', '\r', '\n', ';', '{', '}', '"', '\'':
		return true
	}
	return strings.HasPrefix(l.src[l.pos:], "//") || strings.HasPrefix(l.src[l.pos:], "/*")
}

// skip moves past white space and comments.
func (l *lexer) skip() error {
	for l.pos < len(l.src) {
		rest := l.src[l.pos:]
		switch {
		case rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\r' || rest[0] == '\n':
			l.pos++
		case strings.HasPrefix(rest, "//"):
			end := strings.IndexByte(rest, '\n')
			if end < 0 {
				l.pos = len(l.src)
				return nil
			}
			l.pos += end + 1
		case strings.HasPrefix(rest, "/*"):
			end := strings.Index(rest[2:], "*/")
			if end < 0 {
				return errors.New("a comment is never closed")
			}
			l.pos += 2 + end + 2
		default:
			return nil
		}
	}
	return nil
}

// quoted reads one quoted string and every string "+" joins to it.
func (l *lexer) quoted() (string, error) {
	var b strings.Builder
	for {
		part, err := l.onePart()
		if err != nil {
			return "", err
		}
		b.WriteString(part)
		// A "+" between two quoted strings joins them (RFC 7950 section
		// 6.1.3.1); anything else ends the argument.
		save := l.pos
		if err := l.skip(); err != nil {
			return "", err
		}
		if l.pos >= len(l.src) || l.src[l.pos] != '+' {
			l.pos = save
			return b.String(), nil
		}
		l.pos++
		if err := l.skip(); err != nil {
			return "", err
		}
		if l.pos >= len(l.src) || (l.src[l.pos] != '"' && l.src[l.pos] != '\'') {
			return "", errors.New("a + joins a quoted string to something that is not one")
		}
	}
}

// onePart reads the quoted string at pos: a single-quoted one as written, a
// double-quoted one with its escapes read.
func (l *lexer) onePart() (string, error) {
	quote := l.src[l.pos]
	l.pos++
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		l.pos++
		switch {
		case c == quote:
			return b.String(), nil
		case c == '\\' && quote == '"' && l.pos < len(l.src):
			// The four escapes RFC 7950 section 6.1.3 defines; any other is
			// an error there, kept as written here, since only an argument's
			// equality to a name is judged.
			switch e := l.src[l.pos]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(e)
			}
			l.pos++
		default:
			b.WriteByte(c)
		}
	}
	return "", errors.New("a quoted string is never closed")
}
