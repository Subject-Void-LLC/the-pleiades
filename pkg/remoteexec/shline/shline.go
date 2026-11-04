// Package shline writes a command as a line any login shell passes to sh
// unchanged, and reads one back.
//
// An SSH exec request carries one string, and sshd hands it to the account's
// login shell, which may be sh, bash, zsh, csh, tcsh or fish. Every command
// this platform builds is POSIX shell (if/then, $(...), set -e, a quote closed
// and reopened around an escaped one), and csh and fish read that grammar
// differently or refuse it: fish alters a backslash before a backslash or a
// quote even inside single quotes, and csh refuses a newline inside quotes and
// reads 2>/dev/null as an argument and a redirect. So pkg/remoteexec sends
// every command as
//
//	/bin/sh -c '__pleiades_cmd=$(printf %bX "$1"); set --; eval "unset __pleiades_cmd; ${__pleiades_cmd%X}"' sh '<the command, each unsafe byte as \0NNN>'
//
// whose only special characters, outside the outer quotes' own, are octal
// escapes: no quote, newline, backslash pair, dollar sign or exclamation mark
// survives into the line the login shell reads, so every one of those shells
// hands sh the same two arguments, and sh's printf %b turns the second back
// into the command byte for byte (the X it prints after the command, and cuts
// off again, keeps the trailing newlines command substitution would drop).
// The command sees what it would as sh -c's own: no positional parameters,
// $0 sh, and none of the wrapper's variable, which the eval unsets first.
// A NUL cannot be carried (printf %b drops it, where sshd refuses one), so a
// connection refuses a command holding one. Standard input is untouched, so a command
// that reads its data there still does. The outer sh is named by its path, as
// Ansible names it; a command's own sh (sudo's, a detached job's) is still
// found on the login's PATH. Measured on 2026-11-03 under sh, bash,
// zsh, tcsh and fish: byte-identical output, and a 78 KB command under each.
package shline

import (
	"fmt"
	"strings"
)

// shPrefix and shSuffix surround an encoded command. The prefix holds no byte
// any of the shells above reads differently inside or outside its quotes.
const (
	shPrefix = `/bin/sh -c '__pleiades_cmd=$(printf %bX "$1"); set --; eval "unset __pleiades_cmd; ${__pleiades_cmd%X}"' sh '`
	shSuffix = `'`
)

// MaxBytes is one more than the longest line a connection sends: Linux
// refuses an argument of 128 KiB or more, its closing NUL counted
// (MAX_ARG_STRLEN), and sshd passes the whole line to the login shell as one
// argument.
const MaxBytes = 128 << 10

// shSafe reports whether b may appear as itself in the encoded argument:
// letters, digits, a space and punctuation no shell gives a meaning inside
// single quotes.
func shSafe(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return strings.IndexByte(" _./:,=+-@%", b) >= 0
}

// For returns command as a line any login shell passes to sh unchanged
// (see the package comment).
func For(command string) string {
	return shPrefix + Encode(command) + shSuffix
}

// Encode returns text as For writes it inside its argument: each byte
// outside the safe set as \0 and three octal digits. A masker matches a
// secret's encoded form with it, since a device that echoes the line it was
// sent (a login shell's trace, a ForceCommand's log) echoes it encoded.
func Encode(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if c := text[i]; shSafe(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, `\0%03o`, c)
		}
	}
	return b.String()
}

// From returns the command a line For made carries, and false for any other
// line. A test's SSH server reads what a connection sent with it.
func From(line string) (string, bool) {
	body, ok := strings.CutPrefix(line, shPrefix)
	if !ok {
		return "", false
	}
	body, ok = strings.CutSuffix(body, shSuffix)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		// A backslash is always \0 and three octal digits.
		if i+4 >= len(body) || body[i+1] != '0' {
			return "", false
		}
		var v int
		for _, d := range body[i+2 : i+5] {
			if d < '0' || d > '7' {
				return "", false
			}
			v = v*8 + int(d-'0')
		}
		if v > 0xff {
			return "", false
		}
		b.WriteByte(byte(v))
		i += 4
	}
	return b.String(), true
}
