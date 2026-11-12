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

// FuzzParseDirectives holds ParseDirectives to three properties on any
// input: a refusal reads nothing; a directive stated twice at the top is
// always refused, and, where the rest lexes, the refusal names it and both
// lines; and one more directive stated at the top of a file it reads
// either adds exactly that directive (one the file did not state) or is
// refused as a repeat (one it did), so a later copy never replaces what an
// earlier one said.
func FuzzParseDirectives(f *testing.F) {
	for _, seed := range []string{
		"-- pleiades: no-transaction\nCREATE INDEX CONCURRENTLY i ON t (c);\n",
		"-- pleiades: destructive: drops audit\nDROP TABLE audit;\n",
		"-- pleiades: destructive: a\n-- pleiades: no-transaction\nVACUUM;\n",
		"-- pleiades: no-transaction\n-- pleiades: no-transaction\n",
		"-- pleiades: destructive: a\r\n-- pleiades: destructive: b\r\n",
		"/*\n-- pleiades: no-transaction\n*/\n-- pleiades: no-transaction\n",
		"SELECT '\n-- pleiades: no-transaction\n';\n-- pleiades: no-transaction\n",
		"$$\n-- pleiades: no-transaction\n$$",
		"SELECT 'unbalanced\n-- pleiades: no-transaction\n",
		"\\i x\n",
		"-- pleiades:",
		"-- pleiades: destructive:",
		"-- pleiades:no-transaction\n-- pleiades:   no-transaction   \n",
		"",
	} {
		f.Add(seed)
	}
	directives := []struct {
		name, line string
		add        func(*sqllint.Directives)
	}{
		{"no-transaction", "-- pleiades: no-transaction", func(d *sqllint.Directives) { d.NoTransaction = true }},
		{"destructive", "-- pleiades: destructive: stated at the top", func(d *sqllint.Directives) { d.Destructive = "stated at the top" }},
	}
	f.Fuzz(func(t *testing.T, src string) {
		d, err := sqllint.ParseDirectives(src)
		if err != nil && d != (sqllint.Directives{}) {
			t.Fatalf("the refusal of %q came with %+v", src, d)
		}
		for _, dir := range directives {
			twice := dir.line + "\n" + dir.line + "\n" + src
			if len(twice) > sqllint.MaxSourceBytes {
				continue
			}
			got, err2 := sqllint.ParseDirectives(twice)
			if err2 == nil {
				t.Fatalf("read %q as %+v although it states %s twice", twice, got, dir.name)
			}
			if _, lexErr := sqllint.Split(twice); lexErr == nil {
				if want := "line 2: " + dir.name + " is stated again, first on line 1"; !strings.HasPrefix(err2.Error(), want) {
					t.Fatalf("the refusal of %q is %q, want it to open %q", twice, err2, want)
				}
			}
			if err != nil {
				continue
			}
			once := dir.line + "\n" + src
			got, err3 := sqllint.ParseDirectives(once)
			stated := dir.name == "no-transaction" && d.NoTransaction || dir.name == "destructive" && d.Destructive != ""
			if stated {
				if err3 == nil || !strings.Contains(err3.Error(), dir.name+" is stated again, first on line 1") {
					t.Fatalf("%q, which already states %s, read with one more at the top as %+v, %v", src, dir.name, got, err3)
				}
				continue
			}
			want := d
			dir.add(&want)
			if err3 != nil || got != want {
				t.Fatalf("%q read as %+v; with %s at the top it read as %+v, %v, want %+v", src, d, dir.name, got, err3, want)
			}
		}
	})
}
