package filters

import (
	"fmt"
	"regexp"
	"strings"
)

// windowsDriveAbsPattern matches a Windows drive-absolute path prefix
// ("C:\" or "C:/"), used only by IsAbsolutePath. A bare "C:foo" with no
// separator after the colon is drive-RELATIVE on Windows, not absolute,
// which is why the pattern requires the separator.
var windowsDriveAbsPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// WindowsPathToPOSIX reformats a Windows-style path to use forward
// slashes: a string transform only. It never opens, joins against a
// real base directory, or resolves the result (import_tasks's own path
// boundary is where that hardening lives, not this filter), and it does
// not touch a drive letter ("C:\Users" becomes "C:/Users", not
// "/c/Users" or "/mnt/c/Users"): there is no single POSIX convention for
// a Windows drive letter (WSL, Cygwin and Git Bash each pick a different
// one), so this filter is deliberately scoped to the one part of the
// conversion every convention agrees on.
func WindowsPathToPOSIX(path string) string {
	if len(path) > MaxInputBytes {
		return ""
	}
	return strings.ReplaceAll(path, `\`, "/")
}

// POSIXPathToWindows reformats a POSIX-style path to use backslashes,
// the inverse of WindowsPathToPOSIX; a string transform only, with the
// same scope limits.
func POSIXPathToWindows(path string) string {
	if len(path) > MaxInputBytes {
		return ""
	}
	return strings.ReplaceAll(path, "/", `\`)
}

// IsAbsolutePath reports whether path is absolute under POSIX (a
// leading "/"), Windows UNC (a leading "\\", e.g. "\\server\share"), or
// Windows drive-absolute ("C:\" or "C:/") conventions, since a device
// fact this filter gates on may report either a Linux or a Windows
// path and a runbook author should not need to know which in advance.
// This deliberately does not use path/filepath.IsAbs, whose own answer
// depends on the platform pleiades itself was compiled for rather than
// the path's own convention.
func IsAbsolutePath(path string) bool {
	if len(path) > MaxInputBytes {
		return false
	}
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, "/") {
		return true
	}
	if strings.HasPrefix(path, `\\`) {
		return true
	}
	return windowsDriveAbsPattern.MatchString(path)
}

// permTriplet renders one rwx triplet: digit's bit 4/2/1 are read/
// write/execute, and when special is set the execute position shows
// execChar (execute bit also set) or noExecChar (it is not) instead of
// "x"/"-", the setuid/setgid/sticky convention chmod(1) itself uses.
func permTriplet(digit int, special bool, execChar, noExecChar byte) string {
	var b strings.Builder
	if digit&4 != 0 {
		b.WriteByte('r')
	} else {
		b.WriteByte('-')
	}
	if digit&2 != 0 {
		b.WriteByte('w')
	} else {
		b.WriteByte('-')
	}
	hasExec := digit&1 != 0
	switch {
	case special && hasExec:
		b.WriteByte(execChar)
	case special:
		b.WriteByte(noExecChar)
	case hasExec:
		b.WriteByte('x')
	default:
		b.WriteByte('-')
	}
	return b.String()
}

// OctalToSymbolicPerms converts a 3- or 4-digit octal Unix permission
// string to its 9-character symbolic form ("755" -> "rwxr-xr-x"). A
// 4-digit input's leading digit carries the special bits (4 = setuid, 2
// = setgid, 1 = sticky, OR-able), shown in the owner/group/other
// execute position as "s"/"S" (setuid/setgid, upper when the execute
// bit itself is off) or "t"/"T" (sticky). Returns "" if octal exceeds
// MaxInputBytes, is not 3 or 4 characters, or contains a non-octal-digit
// character.
func OctalToSymbolicPerms(octal string) string {
	if len(octal) > MaxInputBytes {
		return ""
	}
	if len(octal) != 3 && len(octal) != 4 {
		return ""
	}
	digits := make([]int, len(octal))
	for i, c := range octal {
		if c < '0' || c > '7' {
			return ""
		}
		digits[i] = int(c - '0')
	}
	special := 0
	if len(digits) == 4 {
		special = digits[0]
		digits = digits[1:]
	}
	return permTriplet(digits[0], special&4 != 0, 's', 'S') +
		permTriplet(digits[1], special&2 != 0, 's', 'S') +
		permTriplet(digits[2], special&1 != 0, 't', 'T')
}

// parsePermTriplet is permTriplet's inverse: given one rwx-shaped
// triplet (r/-, w/-, and an execute position that may show "x", "-",
// execChar, or noExecChar), it reports the triplet's digit and whether
// its special bit was set. ok is false for any other character in any
// position.
func parsePermTriplet(triplet string, execChar, noExecChar byte) (digit int, special bool, ok bool) {
	if triplet[0] == 'r' {
		digit |= 4
	} else if triplet[0] != '-' {
		return 0, false, false
	}
	if triplet[1] == 'w' {
		digit |= 2
	} else if triplet[1] != '-' {
		return 0, false, false
	}
	switch triplet[2] {
	case 'x':
		digit |= 1
	case execChar:
		digit |= 1
		special = true
	case noExecChar:
		special = true
	case '-':
	default:
		return 0, false, false
	}
	return digit, special, true
}

// SymbolicToOctalPerms converts a 9-character symbolic Unix permission
// string to its octal form, the inverse of OctalToSymbolicPerms: 3
// digits if no special bit is set, 4 if any is. This means
// OctalToSymbolicPerms("0755") round-trips back as "755", not "0755":
// SymbolicToOctalPerms cannot know the original had a redundant leading
// zero, only that no special bit is present, so it always emits the
// shorter canonical form in that case (the same form chmod(1) itself
// prints). The round trip is exact for any octal input whose own digit
// count already matches that rule, which is what this phase's own
// Adversarial Pattern Justification item proves against. Returns "" if
// symbolic exceeds MaxInputBytes, is not exactly 9 characters, or any
// position holds a character its own triplet does not accept.
func SymbolicToOctalPerms(symbolic string) string {
	if len(symbolic) > MaxInputBytes {
		return ""
	}
	if len(symbolic) != 9 {
		return ""
	}
	ownerDigit, ownerSpecial, ok1 := parsePermTriplet(symbolic[0:3], 's', 'S')
	groupDigit, groupSpecial, ok2 := parsePermTriplet(symbolic[3:6], 's', 'S')
	otherDigit, otherSpecial, ok3 := parsePermTriplet(symbolic[6:9], 't', 'T')
	if !ok1 || !ok2 || !ok3 {
		return ""
	}
	special := 0
	if ownerSpecial {
		special |= 4
	}
	if groupSpecial {
		special |= 2
	}
	if otherSpecial {
		special |= 1
	}
	if special == 0 {
		return fmt.Sprintf("%d%d%d", ownerDigit, groupDigit, otherDigit)
	}
	return fmt.Sprintf("%d%d%d%d", special, ownerDigit, groupDigit, otherDigit)
}
