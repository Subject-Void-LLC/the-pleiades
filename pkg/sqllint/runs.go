// Package sqllint: finding the statement that another one runs, so the
// additive-only rule judges what actually runs (a WITH clause's own
// statement, a prepared or explained one, the query a COPY copies out
// of).
package sqllint

import "strings"

// from is the part of s from token start up to end, as a statement of its
// own for destructive to judge. Its Text is left empty: only the tokens
// are read.
func from(s Statement, start, end int) Statement {
	if start >= end || start >= len(s.Tokens) {
		return Statement{Line: s.Line}
	}
	return Statement{Line: s.Tokens[start].Line, Tokens: s.Tokens[start:end]}
}

// within describes a statement that runs another, from how it runs it
// and what the other one does, or "" when the other one is additive.
func within(how, inner string) string {
	if inner == "" {
		return ""
	}
	return how + " " + strings.TrimPrefix(inner, "it ")
}

// isOther reports whether tok is the punctuation text.
func isOther(tok Token, text string) bool { return tok.Kind == Other && tok.Text == text }

// closing is the index of the parenthesis closing the one at open in t,
// or len(t) when it is never closed.
func closing(t []Token, open int) int {
	depth := 0
	for i := open; i < len(t); i++ {
		switch {
		case isOther(t[i], "("):
			depth++
		case isOther(t[i], ")"):
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(t)
}

// statementVerbs are the words a WITH clause's own statement starts with.
var statementVerbs = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"MERGE": true, "VALUES": true, "TABLE": true,
}

// withMain is where the statement a WITH clause is for starts in t, or 0
// when this cannot find it: the first of statementVerbs outside every
// parenthesis, after the first common table expression's parenthesis
// closes, that does not name another one (a name is followed by AS or by
// its column list).
func withMain(t []Token) int {
	depth := 0
	closed := false
	for i, tok := range t {
		switch {
		case isOther(tok, "("):
			depth++
		case isOther(tok, ")"):
			depth--
			closed = closed || depth == 0
		case depth == 0 && closed && tok.Kind == Word && statementVerbs[tok.Text]:
			if i+1 < len(t) && (isWord(t[i+1], "AS") || isOther(t[i+1], "(")) {
				continue
			}
			return i
		}
	}
	return 0
}

// prepared is where the statement PREPARE name [ ( type, ... ) ] AS
// statement prepares starts in t, or 0 when it prepares none (PREPARE
// TRANSACTION, which EndsTransaction answers for).
func prepared(t []Token) int {
	depth := 0
	for i := 1; i < len(t); i++ {
		switch {
		case isOther(t[i], "("):
			depth++
		case isOther(t[i], ")"):
			depth--
		case depth == 0 && isWord(t[i], "AS") && i+1 < len(t):
			return i + 1
		}
	}
	return 0
}

// explained is where the statement EXPLAIN [ ( option, ... ) ] [ANALYZE]
// [VERBOSE] explains starts in t, or 0 when it names none. Without
// ANALYZE an EXPLAIN does not run it, but it is judged all the same: the
// option is one word away, and a file holding either is refused alike.
func explained(t []Token) int {
	i := 1
	if i < len(t) && isOther(t[i], "(") {
		i = closing(t, i) + 1
	}
	for i < len(t) && (isWord(t[i], "ANALYZE") || isWord(t[i], "ANALYSE") || isWord(t[i], "VERBOSE")) {
		i++
	}
	if i >= len(t) {
		return 0
	}
	return i
}

// copyDestructive is Destructive for COPY: one FROM or TO a PROGRAM runs
// that program on the server, and COPY ( statement ) TO is judged by the
// statement it copies out of, depth being the COPY's own nesting.
func copyDestructive(s Statement, depth int) string {
	t := s.Tokens
	parens := 0
	for i := 1; i < len(t); i++ {
		switch {
		case isOther(t[i], "("):
			parens++
		case isOther(t[i], ")"):
			parens--
		case parens == 0 && isWord(t[i], "PROGRAM") && (isWord(t[i-1], "FROM") || isWord(t[i-1], "TO")):
			return "it runs a program on the server, whose effect this cannot see"
		}
	}
	if len(t) > 2 && isOther(t[1], "(") {
		return within("it copies out of a statement that", destructive(from(s, 2, closing(t, 1)), depth+1))
	}
	return ""
}
