// The command line each execution mode sends, and the checks every one
// of them passes before it leaves this process.
//
// The WinRM service starts every command through `cmd.exe /C`, and cannot
// be told not to (cmdexe.go has the measurement). So each mode builds the
// line its own program should receive, and transparentLine then escapes it
// until that cmd.exe passes it through unchanged:
//
//   - ShellNone: the caller's own command line, reaching the program it
//     names exactly as written. CommandLine builds one from an argument
//     vector.
//   - ShellCmd: `cmd.exe /d /v:on /s /c "<script>"`, so the script is read
//     once, by a cmd.exe this package chose the switches for.
//   - ShellPowerShell: `powershell.exe -NoLogo -NoProfile -NonInteractive
//     -EncodedCommand <base64>`, whose script no cmd.exe can read as
//     syntax because base64 has no character it treats as special.
package winrmexec

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Default interpreter paths. They are absolute on purpose: cmd.exe looks
// for a bare program name in the working directory before it searches
// the system directory, so a file named powershell.exe planted in the
// shell's working directory would run in place of the real one. A device
// whose Windows lives somewhere else says so through Options rather than
// through a search.
const (
	// DefaultCmdPath is cmd.exe on a stock Windows installation.
	DefaultCmdPath = `C:\Windows\System32\cmd.exe`
	// DefaultPowerShellPath is Windows PowerShell 5.1 on a stock
	// Windows installation.
	DefaultPowerShellPath = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
)

// MaxCmdLine is cmd.exe's limit on the command it will process (Microsoft
// KB830473), counted in UTF-16 code units because that is what Windows
// counts. It applies to every mode, measured after escaping, because the
// WinRM service passes every line through cmd.exe.
const MaxCmdLine = 8191

// powerShellPrologue runs before every PowerShell script. Progress
// records would otherwise arrive on stderr and read as errors.
const powerShellPrologue = "$ProgressPreference = 'SilentlyContinue'\n"

// powerShellEpilogue runs after every PowerShell script, on its own line.
//
// -EncodedCommand behaves as -Command, whose process exit code is only
// 0 or 1: a native program's exit code of 42 comes back as 1, which
// makes Result.ExitCode a boolean wearing an exit code's clothes. So when
// the script's last statement failed, this exits with the native exit
// code if there is one, and 1 otherwise (a cmdlet failed). A script that
// ends in its own exit statement never reaches this, and a script whose
// last statement succeeded falls through to 0. It deliberately does not
// read $LASTEXITCODE when the last statement succeeded: a native
// failure earlier in the script followed by a successful statement is a
// script that recovered, and exiting with the old code would report it
// as failed.
const powerShellEpilogue = "\nif (-not $?) { if ($LASTEXITCODE) { exit $LASTEXITCODE } else { exit 1 } }"

// commandLine builds the line sent for script under shell: the line its
// program should receive, escaped so the service's cmd.exe passes it on
// unchanged, after checking everything that would otherwise be silently
// changed or rejected on the far side.
func commandLine(shell Shell, script string, opts Options) (string, error) {
	if err := CheckText("script", script); err != nil {
		return "", err
	}
	var line string
	var err error
	switch shell {
	case ShellNone:
		// The script IS the command line: a program and its arguments,
		// already quoted for Windows (CommandLine builds one).
		line = script
	case ShellCmd:
		line, err = cmdLine(script, opts)
	case ShellPowerShell:
		line, err = powerShellLine(script, opts)
	default:
		return "", fmt.Errorf("winrm: unknown shell %v", shell)
	}
	if err != nil {
		return "", err
	}
	sent, err := transparentLine(line)
	if err != nil {
		return "", err
	}
	return checkLength(sent, MaxCmdLine, "the command line cmd.exe receives")
}

// cmdLine builds `cmd.exe /d /v:on /s /c "<script>"`.
//
// /c runs the script and exits. /s makes cmd.exe strip exactly the
// first and last quote of what follows /c and keep everything between
// them byte for byte, which is what lets the script contain quotes of
// its own. /d skips the AutoRun commands the registry can attach to
// every cmd.exe start, cmd.exe's counterpart to PowerShell's -NoProfile:
// a task runs the operator's script and nothing an earlier administrator
// set up to run before every shell.
//
// /v:on turns on delayed expansion, and it is what makes an environment
// variable data in cmd.exe at all. cmd.exe expands %NAME% BEFORE it
// parses the line, so a value containing & or | becomes part of the
// command: measured on Windows 11, a value "a & echo INJECTED" read as
// %NAME% ran the echo. !NAME! is expanded after parsing and stays text.
// So values are read as !NAME!, and checkCmdEnvReads refuses a script
// that reads one of its own values the other way. The cost is that a
// pair of literal exclamation marks in a script must be escaped (^^!).
func cmdLine(script string, opts Options) (string, error) {
	// cmd.exe ends a /c command at a line break, so a multi-line script
	// would run its first line and silently drop the rest.
	if strings.ContainsAny(script, "\r\n") {
		return "", fmt.Errorf("winrm: a cmd script must be one line, because cmd.exe /c stops at the first line break; join commands with & or use shell powershell")
	}
	program, err := programPath(opts.CmdPath, DefaultCmdPath)
	if err != nil {
		return "", err
	}
	return program + ` /d /v:on /s /c "` + script + `"`, nil
}

// checkCmdEnvReads refuses a cmd script that reads one of the command's
// own environment variables as %NAME% (or the %NAME:...% substring form),
// which cmd.exe expands before parsing, so the value would become syntax.
// See cmdLine. Names are compared without regard to case, as cmd.exe
// compares them.
func checkCmdEnvReads(script string, env map[string]string) error {
	lower := strings.ToLower(script)
	for name := range env {
		n := strings.ToLower(name)
		if strings.Contains(lower, "%"+n+"%") || strings.Contains(lower, "%"+n+":") {
			return fmt.Errorf("winrm: the cmd script reads %%%s%%, which cmd.exe expands before it parses the line, so a value containing & or | would run as a command; read it as !%s! instead", name, name)
		}
	}
	return nil
}

// powerShellLine builds `powershell.exe -NoLogo -NoProfile
// -NonInteractive -EncodedCommand <base64>`.
//
// -NoProfile keeps every PowerShell profile script on the device from
// running before the task's own. -NonInteractive makes a prompt such as
// Read-Host fail at once instead of hanging the task until its timeout.
// The script is UTF-16LE, base64 encoded, which is the form
// -EncodedCommand requires, and whose alphabet contains no character any
// Windows parser treats as syntax.
func powerShellLine(script string, opts Options) (string, error) {
	program, err := programPath(opts.PowerShellPath, DefaultPowerShellPath)
	if err != nil {
		return "", err
	}
	wrapped := powerShellPrologue + script + powerShellEpilogue
	encoded := base64.StdEncoding.EncodeToString(utf16LE(wrapped))
	return program + " -NoLogo -NoProfile -NonInteractive -EncodedCommand " + encoded, nil
}

// programPath returns configured, or fallback when configured is empty,
// quoted for the program position of a Windows command line.
//
// The program position is parsed differently from an argument: it may
// be quoted, but a quote inside it has no escape at all. So a path
// containing a quote is refused rather than quoted.
func programPath(configured, fallback string) (string, error) {
	path := configured
	if path == "" {
		path = fallback
	}
	if err := CheckText("program path", path); err != nil {
		return "", err
	}
	if strings.ContainsAny(path, "\"\r\n") {
		return "", fmt.Errorf("winrm: program path %q contains a quote or a line break, which a Windows command line cannot carry in the program position", path)
	}
	if strings.ContainsAny(path, " \t") {
		return `"` + path + `"`, nil
	}
	return path, nil
}

// CommandLine builds a ShellNone command line: program followed by each
// of args, quoted so a program using the standard Windows argument
// parser (CommandLineToArgvW, or the Microsoft C runtime) receives
// exactly args, one element each. The cmd.exe the WinRM service puts in
// front of it is dealt with separately, when the line is sent.
//
// That parser is not the POSIX one. Backslashes are literal except
// immediately before a quote, where 2n backslashes and a quote mean n
// backslashes and the end or start of a quoted run, and 2n+1 mean n
// backslashes and a literal quote. So a quote is escaped by doubling the
// backslashes before it and adding one, and backslashes at the end of a
// quoted argument are doubled so they do not escape the closing quote.
// A program that parses its own command line some other way (cmd.exe is
// the famous one) is not covered, which is why ShellCmd builds its line
// itself. An argument may not hold a line break, which no Windows
// command line can carry.
func CommandLine(program string, args ...string) (string, error) {
	if program == "" {
		return "", fmt.Errorf("winrm: a command line needs a program")
	}
	quoted, err := programPath(program, "")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(quoted)
	for i, arg := range args {
		if err := CheckText(fmt.Sprintf("argument %d", i+1), arg); err != nil {
			return "", err
		}
		if strings.ContainsAny(arg, "\r\n") {
			return "", fmt.Errorf("winrm: argument %d contains a line break, which a Windows command line cannot carry", i+1)
		}
		b.WriteByte(' ')
		b.WriteString(QuoteArg(arg))
	}
	return b.String(), nil
}

// QuoteArg quotes one argument for the standard Windows argument parser.
// See CommandLine for the rule.
func QuoteArg(arg string) string {
	// An argument with no whitespace and no quote needs no quoting, and
	// leaving it bare keeps a command line readable in a log.
	if arg != "" && !strings.ContainsAny(arg, " \t\n\v\"") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			// Held back until we know what follows them.
			backslashes++
		case '"':
			// Double the run before a quote, then escape the quote.
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
			backslashes = 0
		default:
			// Backslashes not before a quote are literal.
			b.WriteString(strings.Repeat(`\`, backslashes))
			b.WriteRune(r)
			backslashes = 0
		}
	}
	// Trailing backslashes precede the closing quote, so they double.
	b.WriteString(strings.Repeat(`\`, backslashes*2))
	b.WriteByte('"')
	return b.String()
}

// CheckText refuses text a WS-Man message cannot carry unchanged.
//
// The message is XML 1.0, which cannot represent most control characters
// at all, and Go's XML escaper replaces each one it meets with U+FFFD
// rather than failing. So a script with an embedded NUL would arrive on
// the device as a different script. Invalid UTF-8 is refused for the
// same reason. what names the value in the error.
func CheckText(what, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("winrm: %s is not valid UTF-8", what)
	}
	for i, r := range s {
		if !isXMLChar(r) {
			return fmt.Errorf("winrm: %s contains U+%04X at byte %d, which a WS-Man message cannot carry", what, r, i)
		}
	}
	return nil
}

// isXMLChar reports whether r is in XML 1.0's Char production.
func isXMLChar(r rune) bool {
	switch {
	case r == '\t' || r == '\n' || r == '\r':
		return true
	case r >= 0x20 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0x10FFFF:
		return true
	}
	return false
}

// checkLength refuses line when it is longer than limit UTF-16 code
// units, naming the limit rather than letting the far side truncate or
// reject it with a message about something else.
func checkLength(line string, limit int, what string) (string, error) {
	if n := utf16Len(line); n > limit {
		return "", fmt.Errorf("winrm: %s would be %d characters and Windows accepts at most %d; pass data on stdin rather than in the script", what, n, limit)
	}
	return line, nil
}

// utf16Len counts s in UTF-16 code units.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// utf16LE encodes s as UTF-16 little endian bytes, the encoding
// -EncodedCommand requires. s has already passed CheckText, so every
// rune is valid and encodes exactly.
func utf16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		// Low byte first: little endian.
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}
