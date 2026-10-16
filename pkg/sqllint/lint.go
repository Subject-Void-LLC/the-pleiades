// Package sqllint: what a statement is, and the directives a migration
// file states about itself.
package sqllint

import (
	"fmt"
	"strings"
)

// Destructive says why a statement is not provably additive: what it
// removes or rewrites, or that it runs something whose effect this cannot
// see. The empty string means it only adds (or only changes rows' values,
// which a backfill does).
//
// It is a rule about the statement's first words and, for one that runs
// another statement (WITH, PREPARE, EXPLAIN, COPY of a query), about the
// statement it runs. It is deliberately strict, failing closed: a DO
// block, a CALL, an EXECUTE, a COPY from or to a program, and a query on
// its own (whose only effect in a migration is what the functions it
// calls do) are refused, since what they do cannot be read here. A
// statement it names that is in fact harmless is waived by the file, with
// a reason, rather than allowed by a looser rule.
//
// What it cannot see and does not refuse is code run by a statement it
// judges by what that statement says it does: a function an INSERT,
// UPDATE or CREATE calls, or a trigger one fires.
func Destructive(s Statement) string { return destructive(s, 0) }

// maxNesting bounds how many statements deep destructive follows one
// statement running another. PostgreSQL nests them only a few deep (an
// EXPLAIN of an EXECUTE, a COPY of a WITH), and following a hostile file
// of thousands, rereading the rest of it at each level, would take time
// that grows with the square of its length.
const maxNesting = 4

// destructive is Destructive for a statement nested depth statements deep
// in the one a file holds.
func destructive(s Statement, depth int) string {
	if depth > maxNesting {
		return "it nests one statement in another more deeply than this reads"
	}
	w := s.Words()
	if len(w) == 0 {
		return ""
	}
	switch w[0] {
	case "DROP":
		return "it drops " + object(w[1:])
	case "TRUNCATE":
		return "it truncates a table"
	case "DELETE":
		return "it deletes rows"
	case "REVOKE":
		return "it revokes a privilege"
	case "WITH":
		for _, word := range w[1:] {
			if word == "DELETE" || word == "TRUNCATE" {
				return "it deletes rows in a WITH clause"
			}
		}
		main := withMain(s.Tokens)
		if main == 0 {
			return "it has a WITH clause whose own statement this cannot find"
		}
		return destructive(from(s, main, len(s.Tokens)), depth+1)
	case "MERGE":
		for _, word := range w[1:] {
			if word == "DELETE" {
				return "it deletes rows in a MERGE"
			}
		}
	case "ALTER":
		return alterDestructive(s)
	case "DO":
		return "it runs a DO block, whose effect this cannot see"
	case "CALL":
		return "it calls a procedure, whose effect this cannot see"
	case "EXECUTE":
		return "it runs a prepared statement, which this cannot see"
	case "PREPARE":
		if body := prepared(s.Tokens); body > 0 {
			return within("it prepares a statement that", destructive(from(s, body, len(s.Tokens)), depth+1))
		}
	case "EXPLAIN":
		if body := explained(s.Tokens); body > 0 {
			return within("it explains, and with ANALYZE runs, a statement that", destructive(from(s, body, len(s.Tokens)), depth+1))
		}
	case "COPY":
		return copyDestructive(s, depth)
	case "SELECT", "VALUES", "TABLE":
		return "it is a query, whose only effect in a migration is what the functions it calls do, which this cannot see"
	}
	return ""
}

// alterDestructive is Destructive for ALTER: a DROP, RENAME or REVOKE
// (ALTER DEFAULT PRIVILEGES) anywhere in it, or a column given a new type.
func alterDestructive(s Statement) string {
	t := s.Tokens
	for i := 1; i < len(t); i++ {
		if t[i].Kind != Word {
			continue
		}
		switch t[i].Text {
		case "DROP":
			return "it drops " + object(words(t[i+1:]))
		case "RENAME":
			return "it renames something"
		case "REVOKE":
			return "it revokes a privilege"
		case "ALTER":
			// ALTER [COLUMN] name [SET DATA] TYPE: the name may be quoted,
			// and is any one token.
			j := i + 1
			if j < len(t) && t[j].Kind == Word && t[j].Text == "COLUMN" {
				j++
			}
			j++
			if j+2 < len(t) && isWord(t[j], "SET") && isWord(t[j+1], "DATA") && isWord(t[j+2], "TYPE") {
				return "it changes a column's type"
			}
			if j < len(t) && isWord(t[j], "TYPE") {
				return "it changes a column's type"
			}
		}
	}
	return ""
}

// object names what a DROP drops, from the words after it.
func object(w []string) string {
	for _, word := range w {
		switch word {
		case "IF", "EXISTS", "CONCURRENTLY":
			continue
		}
		return "a " + strings.ToLower(word)
	}
	return "something"
}

// words is t's bare words.
func words(t []Token) []string {
	var out []string
	for _, tok := range t {
		if tok.Kind == Word {
			out = append(out, tok.Text)
		}
	}
	return out
}

// isWord reports whether tok is the bare word w.
func isWord(tok Token, w string) bool { return tok.Kind == Word && tok.Text == w }

// EndsTransaction reports whether a statement begins, commits or ends a
// transaction, which a migration file run inside one must not do itself.
// ROLLBACK TO a savepoint, SAVEPOINT and RELEASE stay inside it.
func EndsTransaction(s Statement) bool {
	w := s.Words()
	if len(w) == 0 {
		return false
	}
	switch w[0] {
	case "BEGIN", "COMMIT", "END", "ABORT":
		return true
	case "START":
		return len(w) > 1 && w[1] == "TRANSACTION"
	case "PREPARE":
		return len(w) > 1 && w[1] == "TRANSACTION"
	case "ROLLBACK":
		for _, word := range w[1:] {
			if word == "TO" {
				return false
			}
		}
		return true
	}
	return false
}

// OutsideTransaction says why a statement cannot run inside a transaction
// block, or "" when it can: the server refuses these in one, so a file
// holding one must say -- pleiades: no-transaction. It reads the
// statement's own verb and the object right after it, so a column, a
// table or a clause named DATABASE, TABLESPACE or CONCURRENTLY further on
// is not mistaken for one.
func OutsideTransaction(s Statement) string {
	w := s.Words()
	if len(w) == 0 {
		return ""
	}
	switch w[0] {
	case "VACUUM":
		return "VACUUM"
	case "CREATE", "DROP":
		i := 1
		if w[0] == "CREATE" && i < len(w) && w[i] == "UNIQUE" {
			i++
		}
		if i >= len(w) {
			return ""
		}
		switch w[i] {
		case "DATABASE", "TABLESPACE":
			return w[0] + " " + w[i]
		case "INDEX":
			// [CREATE [UNIQUE] | DROP] INDEX CONCURRENTLY ...
			if i+1 < len(w) && w[i+1] == "CONCURRENTLY" {
				return w[0] + " ... CONCURRENTLY"
			}
		}
	case "REINDEX":
		return reindexOutside(s.Tokens)
	case "ALTER":
		if len(w) > 1 && w[1] == "SYSTEM" {
			return "ALTER SYSTEM"
		}
	}
	return ""
}

// reindexOutside is OutsideTransaction for REINDEX [ (option, ...) ]
// kind [CONCURRENTLY] name: a concurrent rebuild of anything, asked for
// after the kind or as an option not set false, and a rebuild of a whole
// schema, database or system, which commits as it goes.
func reindexOutside(t []Token) string {
	i := 1
	concurrently := false
	if i < len(t) && t[i].Kind == Other && t[i].Text == "(" {
		for i++; i < len(t) && !(t[i].Kind == Other && t[i].Text == ")"); i++ {
			if isWord(t[i], "CONCURRENTLY") && (i+1 >= len(t) || !isFalse(t[i+1])) {
				concurrently = true
			}
		}
		i++ // past the closing parenthesis
	}
	if i >= len(t) {
		return ""
	}
	if concurrently || (i+1 < len(t) && isWord(t[i+1], "CONCURRENTLY")) {
		return "REINDEX ... CONCURRENTLY"
	}
	if isWord(t[i], "SCHEMA") || isWord(t[i], "DATABASE") || isWord(t[i], "SYSTEM") {
		return "REINDEX " + t[i].Text
	}
	return ""
}

// isFalse reports whether tok is a boolean option's false: FALSE, OFF or 0.
func isFalse(tok Token) bool {
	return isWord(tok, "FALSE") || isWord(tok, "OFF") || (tok.Kind == Other && tok.Text == "0")
}

// Directives are what a migration file says about itself, in comment
// lines before its first statement:
//
//	-- pleiades: no-transaction
//	-- pleiades: destructive: <why>
type Directives struct {
	// NoTransaction is set when the file must run outside a transaction.
	NoTransaction bool
	// Destructive is the file's stated reason for statements the
	// additive-only rule would refuse; empty when it states none.
	Destructive string
}

// directivePrefix opens a directive comment.
const directivePrefix = "-- pleiades:"

// ParseDirectives reads src's directives: its -- comments that open like
// one, before its first statement. One that names no directive this reads
// is refused, so a misspelled no-transaction is not quietly a
// transactional file, as is one after the first statement, which would
// read as if it applied. A comment inside a string, or within a /* */
// comment, is not a directive.
func ParseDirectives(src string) (Directives, error) {
	_, comments, err := scan(src)
	if err != nil {
		return Directives{}, err
	}
	var d Directives
	for _, c := range comments {
		text := strings.TrimRight(c.text, "\r")
		if !strings.HasPrefix(text, directivePrefix) {
			continue
		}
		if c.afterToken {
			return Directives{}, fmt.Errorf("line %d: a %s directive after the first statement applies to nothing; move it to the top", c.line, directivePrefix)
		}
		name, value, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(text, directivePrefix)), ":")
		switch strings.TrimSpace(name) {
		case "no-transaction":
			if strings.TrimSpace(value) != "" {
				return Directives{}, fmt.Errorf("line %d: no-transaction takes no value", c.line)
			}
			d.NoTransaction = true
		case "destructive":
			if d.Destructive = strings.TrimSpace(value); d.Destructive == "" {
				return Directives{}, fmt.Errorf("line %d: destructive needs a reason: %s destructive: <why>", c.line, directivePrefix)
			}
		default:
			return Directives{}, fmt.Errorf("line %d: %q is not a directive (no-transaction, destructive: <why>)", c.line, strings.TrimSpace(name))
		}
	}
	return d, nil
}
