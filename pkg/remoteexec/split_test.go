package remoteexec

import (
	"strings"
	"testing"
)

// TestSplitWords covers the grammar SplitWords implements and, just as
// importantly, the parts of a shell it refuses to implement: an operator
// or an expansion must survive as a literal character in an argument,
// never as syntax.
func TestSplitWords(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "only whitespace", in: "  \t\n ", want: nil},
		{name: "single word", in: "uname", want: []string{"uname"}},
		{name: "words and flags", in: "systemctl restart nginx", want: []string{"systemctl", "restart", "nginx"}},
		{name: "runs of whitespace collapse", in: "  a \t\t b  ", want: []string{"a", "b"}},
		{name: "single quotes hold a space", in: `touch '/tmp/two words'`, want: []string{"touch", "/tmp/two words"}},
		{name: "double quotes hold a space", in: `touch "/tmp/two words"`, want: []string{"touch", "/tmp/two words"}},
		{name: "an empty quoted argument survives", in: `echo ''`, want: []string{"echo", ""}},
		{name: "quotes may abut a bare word", in: `--name='my app'`, want: []string{"--name=my app"}},
		{name: "single quotes are fully literal", in: `echo '$HOME \n "x"'`, want: []string{"echo", `$HOME \n "x"`}},
		{name: "backslash escapes a space", in: `touch /tmp/two\ words`, want: []string{"touch", "/tmp/two words"}},
		{name: "backslash escapes a quote", in: `echo it\'s`, want: []string{"echo", "it's"}},
		{
			name: "backslash inside double quotes is literal before an ordinary byte",
			in:   `echo "C:\path\to"`,
			want: []string{"echo", `C:\path\to`},
		},
		{
			name: "backslash inside double quotes escapes the special set",
			in:   `echo "a\"b\\c\$d"`,
			want: []string{"echo", `a"b\c$d`},
		},
		{
			// The security-relevant case. An operator must come back as a
			// character inside an argument, never as a second command.
			name: "an operator is a literal character",
			in:   "echo hi; rm -rf /",
			want: []string{"echo", "hi;", "rm", "-rf", "/"},
		},
		{
			name: "command substitution is a literal argument",
			in:   "echo $(whoami)",
			want: []string{"echo", "$(whoami)"},
		},
		{
			name: "a pipe is a literal argument",
			in:   "cat /etc/passwd | mail attacker",
			want: []string{"cat", "/etc/passwd", "|", "mail", "attacker"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitWords(tc.in)
			if err != nil {
				t.Fatalf("SplitWords(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("SplitWords(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("SplitWords(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// TestSplitWords_RefusesAnAmbiguousLine proves an unterminated quote or a
// dangling backslash is an error rather than a guess. Both plausible
// guesses produce different argument vectors and neither is what the
// author meant, so the module refuses and says which byte is at fault.
func TestSplitWords_RefusesAnAmbiguousLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "unterminated single quote", in: `echo 'oops`, want: "unterminated single quote"},
		{name: "unterminated double quote", in: `echo "oops`, want: "unterminated double quote"},
		{name: "dangling backslash", in: `echo oops\`, want: "dangling backslash"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitWords(tc.in)
			if err == nil {
				t.Fatalf("SplitWords(%q) = %q, want an error", tc.in, got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			if got != nil {
				t.Errorf("SplitWords returned %q alongside an error; a partial vector would run the wrong command", got)
			}
		})
	}
}

// FuzzSplitWordsRoundTrip is the property that makes the split-then-quote
// pipeline safe: whatever SplitWords produces, QuoteCommand must turn
// back into exactly that same vector when a shell reads it.
//
// A break here is remote command injection. If splitting produced an
// argument that quoting failed to neutralize, or if quoting produced a
// line that re-split differently, a metacharacter from a runbook file
// would reach the remote shell as syntax.
func FuzzSplitWordsRoundTrip(f *testing.F) {
	f.Add("systemctl restart nginx")
	f.Add("echo hi; rm -rf /")
	f.Add(`touch '/tmp/two words'`)
	f.Add(`echo "$(whoami)"`)
	f.Add("")
	f.Add(`a\ b`)
	f.Add("'")
	f.Add(`"`)
	f.Add("\\")
	f.Add("echo\tx\ny")
	f.Add("`id`")

	f.Fuzz(func(t *testing.T, line string) {
		argv, err := SplitWords(line)
		if err != nil {
			// A refused line never reaches QuoteCommand, so there is
			// nothing further to assert about it.
			return
		}

		quoted := QuoteCommand(argv)
		if len(argv) == 0 {
			if quoted != "" {
				t.Fatalf("QuoteCommand(nil) = %q, want the empty string", quoted)
			}
			return
		}

		got := splitQuotedLine(t, quoted)
		if len(got) != len(argv) {
			t.Fatalf("SplitWords(%q) = %q, which QuoteCommand rendered as %q, which a shell reads back as %q", line, argv, quoted, got)
		}
		for i := range got {
			if got[i] != argv[i] {
				t.Fatalf("argument %d round-tripped as %q, want %q (line %q, quoted %q)", i, got[i], argv[i], line, quoted)
			}
		}
	})
}
