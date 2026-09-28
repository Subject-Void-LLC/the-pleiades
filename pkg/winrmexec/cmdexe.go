// Making the WinRM service's own cmd.exe transparent.
//
// # The service always runs a command through cmd.exe
//
// [MS-WSMV] 3.1.4.11 defines WINRS_SKIP_CMD_SHELL for asking the WinRM
// service to start a command without cmd.exe. Windows does not honor it.
// Measured on 2026-09-25 against Windows 11 build 26200: with the option
// set to TRUE, %CMDCMDLINE% inside the command showed it had been started
// as `cmd.exe /C <line>` in every shape the Command element can take (the
// whole line in Command, or the program in Command and each argument in
// its own Arguments element), and the same option marked MustComply was
// refused as not valid. So every line this package sends is read by
// cmd.exe before the program it names ever sees it, whichever mode it is.
//
// # So every line is escaped until cmd.exe passes it through unchanged
//
// `cmd.exe /C` reads the rest of its command line in three steps that
// change it:
//
//  1. A line that begins with a double quote loses its first and last
//     double quote (the rule cmd /? calls the old behavior, which applies
//     whenever the line holds more than two quotes).
//  2. %NAME% is replaced by the value of NAME, when NAME is defined.
//  3. Outside double quotes, & | < > ( ) end or redirect the command, and
//     ^ makes the character after it literal and is removed.
//
// transparentLine undoes all three in advance, so what reaches
// CreateProcess is the line exactly as written: every metacharacter after
// the program gets a caret, including each double quote (an escaped quote
// does not start a quoted run, so nothing after it is ever read as
// quoted), and each percent sign (in command-line mode `^%NAME^%` names a
// variable called "NAME^", which is never defined, so cmd.exe leaves it
// alone and then removes the carets). A line that begins with a quoted
// program gets one more pair of quotes around it, for step 1 to remove.
// This is the approach the Rust standard library took for the same
// problem after CVE-2024-24576, where a program run through cmd.exe
// without it let an argument run as a command.
//
// Two things are refused rather than escaped: a line break, which ends
// the command, and % or ! in the program's own path, since inside the
// quotes around a path cmd.exe still expands both and there is no escape
// that works there.
//
// One thing is outside what escaping can promise: a host whose command
// processor has delayed expansion turned on in the registry expands
// !NAME! after the carets are gone. Windows ships with it off.
package winrmexec

import (
	"fmt"
	"strings"
)

// cmdMetacharacters are the characters escaped after the program, each
// with a caret: the ones cmd.exe acts on outside quotes, the caret
// itself, the double quote, and the two expansion introducers.
const cmdMetacharacters = `^&|<>()"%!`

// transparentLine returns line escaped so that `cmd.exe /C` passes it to
// CreateProcess exactly as written. See the file doc for the rules.
func transparentLine(line string) (string, error) {
	if strings.ContainsAny(line, "\r\n") {
		return "", fmt.Errorf("winrm: a command line cannot contain a line break: cmd.exe, which the WinRM service always starts a command through, stops at the first one")
	}
	program, rest, err := splitProgram(line)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	quoted := strings.HasPrefix(program, `"`)
	if quoted {
		// Removed again by cmd.exe's first step, which would otherwise
		// take the program's own opening quote and the line's last one.
		b.WriteByte('"')
	}
	b.WriteString(program)
	// Byte by byte: every metacharacter is ASCII, and copying bytes keeps
	// everything else exactly as it was, valid UTF-8 or not.
	for i := 0; i < len(rest); i++ {
		if strings.IndexByte(cmdMetacharacters, rest[i]) >= 0 {
			b.WriteByte('^')
		}
		b.WriteByte(rest[i])
	}
	if quoted {
		b.WriteByte('"')
	}
	return b.String(), nil
}

// splitProgram separates a command line's program from the rest, and
// refuses a program cmd.exe would read differently from how it is
// written.
func splitProgram(line string) (program, rest string, err error) {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return "", "", fmt.Errorf("winrm: a command line must begin with its program")
	}
	if strings.HasPrefix(line, `"`) {
		end := strings.IndexByte(line[1:], '"')
		if end < 0 {
			return "", "", fmt.Errorf("winrm: the program's opening quote is never closed")
		}
		program, rest = line[:end+2], line[end+2:]
		if strings.ContainsAny(program, "%!") {
			return "", "", fmt.Errorf("winrm: program path %s contains %% or !, which cmd.exe expands even inside quotes", program)
		}
		return program, rest, nil
	}
	end := strings.IndexAny(line, " \t")
	if end < 0 {
		end = len(line)
	}
	program, rest = line[:end], line[end:]
	// Unquoted, the program is split and interpreted by cmd.exe like any
	// other text, and a caret cannot be added without changing the name
	// it resolves; these must be quoted or avoided.
	if strings.ContainsAny(program, cmdMetacharacters+",;=") {
		return "", "", fmt.Errorf("winrm: program %q contains a character cmd.exe interprets; quote the path, or use one without %s", program, cmdMetacharacters+",;=")
	}
	return program, rest, nil
}
