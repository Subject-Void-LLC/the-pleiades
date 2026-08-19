package remoteexec

import (
	"strings"
	"testing"
)

// TestQuoteArg covers the shapes that break naive quoting, including the
// one byte the single-quoted form cannot contain literally.
func TestQuoteArg(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain word", in: "pong", want: "'pong'"},
		{name: "empty string", in: "", want: "''"},
		{name: "embedded single quote", in: "it's", want: `'it'\''s'`},
		{name: "shell metacharacters neutralized", in: "pong; rm -rf /", want: "'pong; rm -rf /'"},
		{name: "command substitution neutralized", in: "$(whoami)", want: "'$(whoami)'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QuoteArg(tt.in); got != tt.want {
				t.Errorf("QuoteArg(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestQuoteCommand proves an argument vector becomes one command line
// with every element quoted independently, so a value containing a space
// stays one argument rather than splitting into two.
func TestQuoteCommand(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{name: "empty argv", argv: nil, want: ""},
		{name: "single element", argv: []string{"uname"}, want: "'uname'"},
		{name: "flag and value", argv: []string{"uname", "-a"}, want: "'uname' '-a'"},
		{
			name: "a value containing a space stays one argument",
			argv: []string{"touch", "/tmp/two words"},
			want: `'touch' '/tmp/two words'`,
		},
		{
			name: "a value that looks like a second command is not one",
			argv: []string{"echo", "hi; rm -rf /"},
			want: `'echo' 'hi; rm -rf /'`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QuoteCommand(tt.argv); got != tt.want {
				t.Errorf("QuoteCommand(%q) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
}

// unquotePOSIXSingle is a minimal reference implementation of how a POSIX
// shell reads the single-quoted form QuoteArg produces. It exists so the
// fuzz test below can assert the real property that matters (the remote
// shell sees back exactly the bytes the caller supplied) rather than a
// weaker structural property like "starts and ends with a quote."
//
// The grammar it implements is deliberately tiny, because the grammar
// QuoteArg targets is tiny: outside quotes, a quote opens a quoted run;
// inside a quoted run every byte is literal until the next quote, which
// closes it. That is what makes the escape sequence work: it closes the
// run, contributes an escaped literal quote, then reopens.
func unquotePOSIXSingle(t *testing.T, quoted string) string {
	t.Helper()

	var out strings.Builder
	inQuotes := false
	for i := 0; i < len(quoted); i++ {
		c := quoted[i]
		switch {
		case c == '\'':
			inQuotes = !inQuotes
		case c == '\\' && !inQuotes:
			// Outside quotes a backslash escapes exactly one byte, which is
			// how the middle of the escape sequence contributes a literal
			// single quote.
			if i+1 < len(quoted) {
				i++
				out.WriteByte(quoted[i])
			}
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// FuzzQuoteArg is this package's injection-boundary fuzz target.
//
// Module parameters are author-controlled text from a runbook file that
// ends up interpolated into a command line run on a remote device, so
// the property under test is the security-relevant one: whatever bytes
// go in, a POSIX shell must see exactly those bytes back as ONE
// argument, never as shell syntax. A regression here is remote command
// injection, not a formatting bug.
func FuzzQuoteArg(f *testing.F) {
	f.Add("pong")
	f.Add("")
	f.Add("'")
	f.Add("''")
	f.Add("; rm -rf /")
	f.Add("$(whoami)")
	f.Add("`id`")
	f.Add("a'; touch /tmp/pwned; echo '")
	f.Add("$USER")
	f.Add("\\")
	f.Add("multi\nline")
	f.Add("nul\x00byte")

	f.Fuzz(func(t *testing.T, data string) {
		quoted := QuoteArg(data)

		// The result must always be a fully quoted token: an odd number of
		// structural quotes would leave the remote shell parsing the rest
		// of the command line in an unintended state.
		if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
			t.Fatalf("QuoteArg(%q) = %q, want it wrapped in single quotes", data, quoted)
		}

		if got := unquotePOSIXSingle(t, quoted); got != data {
			t.Fatalf("QuoteArg(%q) = %q, which a POSIX shell reads back as %q", data, quoted, got)
		}
	})
}

// FuzzQuoteCommand extends the same property to a whole argument vector:
// two adversarial elements must come back as exactly two arguments, with
// neither able to escape into the other or into the command line around
// them. QuoteArg being correct on its own does not prove the join is,
// since the separator is what an escaped element would attack.
func FuzzQuoteCommand(f *testing.F) {
	f.Add("echo", "hi")
	f.Add("echo", "'")
	f.Add("touch", "/tmp/a b")
	f.Add("sh", "-c 'id'")
	f.Add("", "")
	f.Add("a'", "'b")

	f.Fuzz(func(t *testing.T, first, second string) {
		line := QuoteCommand([]string{first, second})

		got := splitQuotedLine(t, line)
		if len(got) != 2 {
			t.Fatalf("QuoteCommand([%q %q]) = %q, which a POSIX shell splits into %d arguments, want 2", first, second, line, len(got))
		}
		if got[0] != first || got[1] != second {
			t.Fatalf("QuoteCommand([%q %q]) = %q, which a POSIX shell reads back as %q", first, second, line, got)
		}
	})
}

// splitQuotedLine is unquotePOSIXSingle extended with the one further
// rule a whole command line needs: an unquoted space separates
// arguments. It is the same tiny grammar, and it is what lets the fuzz
// test above assert argument COUNT and not just content.
func splitQuotedLine(t *testing.T, line string) []string {
	t.Helper()

	var args []string
	var current strings.Builder
	inQuotes := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\'':
			inQuotes = !inQuotes
		case c == '\\' && !inQuotes:
			if i+1 < len(line) {
				i++
				current.WriteByte(line[i])
			}
		case c == ' ' && !inQuotes:
			args = append(args, current.String())
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	args = append(args, current.String())
	return args
}
