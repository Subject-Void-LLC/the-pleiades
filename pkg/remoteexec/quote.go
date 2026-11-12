package remoteexec

import "strings"

// QuoteArg single-quotes s for safe inclusion in a POSIX remote command
// line, rewriting any embedded single quote into the four-byte sequence
// quote, backslash, quote, quote.
//
// This is the injection boundary of every SSH-backed module. An SSH exec
// request carries one string, and the shell on the far side parses it (sh,
// to which a connection hands every command whatever the login shell is:
// shline), so a module that pastes a runbook-authored value straight into
// it has handed the runbook's author a remote shell. The
// values in question are ordinary module parameters (a filename, a
// package name, a message to echo), and any of them can contain a
// semicolon, a backtick or a dollar sign.
//
// The single-quote form is used because its grammar is the smallest one
// a shell has: inside a single-quoted run every byte is literal, with no
// escapes and no expansion of any kind, until the next single quote. The
// one byte that cannot appear inside such a run is the quote itself, so
// an embedded quote closes the run, contributes an escaped literal quote
// outside it, and reopens. The result is always exactly one argument to
// sh containing exactly the bytes that went in. It is POSIX quoting: fish
// and csh read it differently, which is why a connection does not hand
// its commands to them (shline), and why a Raw connection's commands, for a
// login shell that is not POSIX, are the caller's to write.
func QuoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DirOperand returns dir as the operand of a POSIX cd: a relative directory
// gets "./" before it, which names the same directory and leaves cd nothing
// else to read it as. Quote the result with QuoteArg as any other word.
//
// cd gives a relative operand two meanings that quoting and "--" do not
// stop. "-" is $OLDPWD, which dash and BusyBox's sh, with no OLDPWD, read as
// "stay here" and succeed (FAILURE_PATTERNS 1179). And a name not beginning
// with "/", "." or ".." is searched for under each entry of $CDPATH, which
// the device's login environment can set, before the current directory, and
// when found there cd prints the path it entered. Measured on dash, bash and
// BusyBox's sh: with CDPATH naming another directory holding proj,
// cd -- 'proj' entered that one, and cd -- './proj' entered ./proj. An empty
// dir, which callers read as "do not change directory", and an absolute one
// are returned as they are.
func DirOperand(dir string) string {
	if dir == "" || strings.HasPrefix(dir, "/") {
		return dir
	}
	return "./" + dir
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
