// Fuzzing for the lexer: whatever the input, Split ends, and what it
// returns is consistent with the source.
package sqllint_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sqllint"
)

// FuzzSplit holds Split to three properties on any input: it returns or
// refuses without panicking; every statement it returns is non-empty
// text found in the source, in order, on a line the source has; and the
// rules run over every statement without panicking.
func FuzzSplit(f *testing.F) {
	for _, seed := range []string{
		"SELECT 1; SELECT 2",
		"CREATE TABLE \"a;b\" (c text DEFAULT 'x;y');",
		"SELECT $$ ; $$, $t$ $$ $t$, $1",
		"/* a /* b */ c */ DROP TABLE t",
		"ALTER TABLE t ALTER COLUMN c TYPE int; -- pleiades: x",
		"SELECT E'\\'', U&'\\0061'",
		"\\i x",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		stmts, err := sqllint.Split(src)
		if err != nil {
			return
		}
		lines := strings.Count(src, "\n") + 1
		at := 0
		for _, s := range stmts {
			if s.Text == "" || len(s.Tokens) == 0 {
				t.Fatalf("an empty statement from %q", src)
			}
			i := strings.Index(src[at:], s.Text)
			if i < 0 {
				t.Fatalf("statement %q is not in the source after offset %d", s.Text, at)
			}
			at += i + len(s.Text)
			if s.Line < 1 || s.Line > lines {
				t.Fatalf("statement on line %d of %d", s.Line, lines)
			}
			_ = sqllint.Destructive(s)
			_ = sqllint.EndsTransaction(s)
			_ = sqllint.OutsideTransaction(s)
		}
		_, _ = sqllint.ParseDirectives(src)
	})
}
