package remoteexec

import "strings"

// QuoteArg single-quotes s for safe inclusion in a POSIX remote command
// line, rewriting any embedded single quote into the four-byte sequence
// quote, backslash, quote, quote.
//
// This is the injection boundary of every SSH-backed module. An SSH exec
// request carries one string, and the remote sshd hands that string to
// the login shell, so a module that pastes a runbook-authored value
// straight into it has handed the runbook's author a remote shell. The
// values in question are ordinary module parameters (a filename, a
// package name, a message to echo), and any of them can contain a
// semicolon, a backtick or a dollar sign.
//
// The single-quote form is used because its grammar is the smallest one
// a shell has: inside a single-quoted run every byte is literal, with no
// escapes and no expansion of any kind, until the next single quote. The
// one byte that cannot appear inside such a run is the quote itself, so
// an embedded quote closes the run, contributes an escaped literal quote
// outside it, and reopens. The result is always exactly one argument
// containing exactly the bytes that went in.
func QuoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteCommand joins argv into one shell-safe command line, quoting
// every element with QuoteArg.
//
// It is what turns an argument vector into the single string the SSH
// exec channel takes, which is the honest way to run a command "with no
// shell involved" over a protocol that only accepts a shell command:
// there is a shell on the far side either way, and quoting every element
// is what stops it from finding anything to interpret. An empty argv
// returns an empty string, which callers must refuse rather than send.
func QuoteCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = QuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}
