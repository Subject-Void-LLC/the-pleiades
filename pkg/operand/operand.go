// Package operand holds the one rule for a value a Collection places on a
// device's command line as an operand: the name of one package, unit,
// service, account or group, written after the program's own options.
//
// # Why quoting is not enough
//
// pkg/remoteexec.QuoteArg makes a value one word that no shell interprets.
// It cannot stop the program that receives the word from reading it as
// something other than a name, and two kinds of word are read that way by
// every program this platform passes a name to:
//
//   - A word beginning with "-" is an option. Measured on Debian bookworm:
//     dpkg-query refuses "-oX" as an unknown option (exit 2), which
//     pkg.apt.install read as "not installed", and apt-get install -y then
//     took "-oDPkg::Pre-Invoke::=touch /tmp/pwned" as a configuration option
//     and exited 0 (FAILURE_PATTERNS 1174). dnf, rpm and systemctl parse
//     options the same way, and so do chown and chgrp: GNU chown took an
//     owner of --reference=/etc/shadow as "give the file /etc/shadow's owner
//     and group" and exited 0 (measured on Debian 13). So does glibc's
//     getent, where an account named -i made getent passwd -i list every
//     account and exit 0, and the first record, root's, read as the account
//     asked for. getent is also why "--" is not enough on its own: musl's
//     getent takes no options and reads "--" as a key, so identity.user.*
//     and identity.group.* cannot send one to it, and this rule is all that
//     stands between a name and glibc getent's options.
//   - A word holding "*", "?", "[" or "]" is a pattern. apt-get, dnf and
//     systemctl expand one to every name it matches, and so does
//     PowerShell's -Name on Get-Service, Start-Service, Stop-Service and
//     Restart-Service. Measured on Rocky Linux 9: systemctl show "s*" prints
//     the state of every loaded unit whose name starts with s, sshd and
//     systemd-journald among them, and dnf remove "openssh*" plans the
//     removal of both openssh packages.
//
// No real package, systemd unit or Windows service a method could act on
// one at a time has a name like either, and neither does an account or a
// group: shadow-utils' useradd and groupadd refuse both (measured on Debian
// 13 and Alpine 3.22). So refusing them costs nothing that works today. A "--" before the operands, where the program takes one,
// stops the first kind as well and is added beside this rule; nothing stops
// the second but refusing it.
//
// # Where the rule is applied
//
// Problem is the rule itself. pkg/collection holds a parameter declaring
// collection.ParamFormatOperand to it at validation (a literal) and in the
// engine (a rendered value), and each method holds the value it reads to it
// again through pkg/sdk before anything is sent, which covers a call that
// reached the method some other way (a generic dispatcher, a replayed undo).
package operand

import (
	"fmt"
	"strings"
)

// patternCharacters are the characters a program reading names expands as
// a pattern: the shell-style wildcards apt-get, dnf and systemctl match
// against their names, and PowerShell's wildcard set.
const patternCharacters = "*?[]"

// Problem returns "" when text can only be read as the one name it holds,
// and otherwise a sentence saying why not.
//
// The sentence never repeats the value, because the value may have been
// rendered from what a device returned, and what an author needs to fix it
// is the rule, not the text.
func Problem(text string) string {
	switch {
	case strings.HasPrefix(text, "-"):
		// The program's option parser sees this before any name lookup does.
		return `it begins with "-", which the program would read as an option rather than as a name`
	case strings.ContainsAny(text, patternCharacters):
		// apt-get, dnf, systemctl and PowerShell's -Name each expand these.
		return `it holds "*", "?", "[" or "]", which the program would read as a pattern matching many names rather than as one name`
	}
	return ""
}

// Check returns an error naming key when text fails Problem, and nil when it
// passes. It is the form a method calls on a value it is about to send.
func Check(key, text string) error {
	if problem := Problem(text); problem != "" {
		return fmt.Errorf("%s is refused: %s", key, problem)
	}
	return nil
}
