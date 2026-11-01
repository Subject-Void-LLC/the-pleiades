// Package remoteexec: a fuzz test of the sudo prefix become puts in front of
// every command, so that neither the account name nor the command can become
// a second word, an option or a second command.
package remoteexec

import (
	"strings"
	"testing"
)

// FuzzBecomeElevate holds the one property the sudo prefix must keep for any
// account name and any command: either the name is refused before anything
// is built, or the line reads back, through the single-quote splitter
// FuzzQuoteCommand uses (quote_test.go: quotes, backslashes and spaces, not a
// whole shell), as exactly sudo -n -u NAME -- sh -c COMMAND, eight words,
// with the name never read as an option. Every word is single-quoted
// (QuoteArg), which is what makes that splitter a fair model here. The
// seeds are shell breakouts (CWE-78), the Unicode quotes some shells fold to
// an ASCII quote, an option where a name goes, and a newline, which a
// multi-line regular expression would let through.
func FuzzBecomeElevate(f *testing.F) {
	for _, seed := range []struct{ user, command string }{
		{"", "true"},
		{"netadmin", `echo 'a b' | tr a-z A-Z > "$HOME/x"`},
		{"root; id", "true"},
		{"root|id", "true"},
		{"$(id)", "true"},
		{"`id`", "true"},
		{"-u", "true"},
		{"--", "true"},
		{"root\nid", "true"},
		{"root\n", "true"},
		{"root ", "true"},
		{"r\x00oot", "true"},
		{"root'\xe2\x80\x98; id #", "true"},
		{"root", "'\xe2\x80\x99; id #"},
		{"root", "'; id #"},
		{"root", "& whoami"},
		{strings.Repeat("a", 32), "true"},
		{strings.Repeat("a", 33), "true"},
	} {
		f.Add(seed.user, seed.command)
	}

	f.Fuzz(func(t *testing.T, user, command string) {
		b := Become{User: user}
		if !ValidBecomeUser(b.target()) {
			// WithBecome refuses this name before it builds or sends
			// anything, which TestWithBecome_RefusesWhatCannotBeElevated
			// proves against a fake device; there is no line to check.
			return
		}
		// A name the check accepts can never start an option or carry a
		// byte a shell or sudo treats as more than a letter.
		name := b.target()
		if strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\n\r'\"\\$`;|&<>(){}*?![]#~=%\x00") {
			t.Fatalf("ValidBecomeUser accepted %q, which holds a byte a shell or sudo would read", name)
		}

		c := &Conn{become: &b}
		line := c.elevate(command)
		got := splitQuotedLine(t, line)
		want := []string{"sudo", "-n", "-u", name, "--", "sh", "-c", command}
		if len(got) != len(want) {
			t.Fatalf("elevate(%q) as %q = %q, which a POSIX shell splits into %d words, want %d", command, user, line, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("elevate(%q) as %q = %q, word %d reads back as %q, want %q", command, user, line, i, got[i], want[i])
			}
		}
	})
}
