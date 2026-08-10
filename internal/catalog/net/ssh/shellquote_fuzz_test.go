package ssh

import (
	"strings"
	"testing"
)

// unquotePOSIXSingle is a minimal reference implementation of how a POSIX
// shell reads the single-quoted form shellQuote produces. It exists so the
// fuzz test below can assert the real property that matters (the remote
// shell sees back exactly the bytes the runbook author wrote) rather than
// a weaker structural property like "starts and ends with a quote."
//
// The grammar it implements is deliberately tiny, because the grammar
// shellQuote targets is tiny: outside quotes, a "'" opens a quoted run;
// inside a quoted run every byte is literal until the next "'", which
// closes it. That is what makes '\” work: it closes the run, contributes
// an escaped literal quote, then reopens.
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
			// Outside quotes a backslash escapes exactly one byte, which
			// is how the middle of the '\'' sequence contributes a literal
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

// FuzzShellQuote is this phase's injection-boundary fuzz target.
// params.data is author-controlled text from a runbook file that ends up
// interpolated into a command line run on a remote device, so the property
// under test is the security-relevant one: whatever bytes go in, a POSIX
// shell must see exactly those bytes back as ONE argument, never as shell
// syntax. A regression here is remote command injection, not a formatting
// bug.
func FuzzShellQuote(f *testing.F) {
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
		quoted := shellQuote(data)

		// The result must always be a fully quoted token: an odd number of
		// structural quotes would leave the remote shell parsing the rest
		// of the command line in an unintended state.
		if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
			t.Fatalf("shellQuote(%q) = %q, want it wrapped in single quotes", data, quoted)
		}

		if got := unquotePOSIXSingle(t, quoted); got != data {
			t.Fatalf("shellQuote(%q) = %q, which a POSIX shell reads back as %q", data, quoted, got)
		}
	})
}
