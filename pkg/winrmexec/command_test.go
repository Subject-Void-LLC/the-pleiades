// Tests for the command line each execution mode builds.
//
// The standard Windows argument parser is modeled here in Go
// (parseWindowsArgs) so CommandLine's promise, that a program receives
// exactly the arguments it was given, can be checked as a property and
// fuzzed. The model is not the evidence that Windows agrees: the WinRM
// Release Gate runs a real program on a real host and compares the
// argument vector it received.
package winrmexec

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

// parseWindowsArgs models how CommandLineToArgvW and the Microsoft C
// runtime split a command line: the program token first, where a quote
// only toggles quoting, then each argument, where 2n backslashes before a
// quote are n backslashes and a quote toggle, 2n+1 are n backslashes and
// a literal quote, and a backslash anywhere else is literal.
func parseWindowsArgs(line string) (program string, args []string) {
	runes := []rune(line)
	i := 0
	// Program token: quotes toggle, nothing escapes.
	var prog strings.Builder
	inQuotes := false
	for ; i < len(runes); i++ {
		r := runes[i]
		if r == '"' {
			inQuotes = !inQuotes
			continue
		}
		if !inQuotes && (r == ' ' || r == '\t') {
			break
		}
		prog.WriteRune(r)
	}
	program = prog.String()
	for {
		// Skip whitespace between arguments.
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
		if i >= len(runes) {
			return program, args
		}
		var arg strings.Builder
		inQuotes = false
		for i < len(runes) {
			r := runes[i]
			if r == '\\' {
				n := 0
				for i < len(runes) && runes[i] == '\\' {
					n++
					i++
				}
				if i < len(runes) && runes[i] == '"' {
					arg.WriteString(strings.Repeat(`\`, n/2))
					if n%2 == 1 {
						arg.WriteRune('"')
						i++
					}
				} else {
					arg.WriteString(strings.Repeat(`\`, n))
				}
				continue
			}
			if r == '"' {
				inQuotes = !inQuotes
				i++
				continue
			}
			if !inQuotes && (r == ' ' || r == '\t') {
				break
			}
			arg.WriteRune(r)
			i++
		}
		args = append(args, arg.String())
	}
}

func TestQuoteArg(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: "plain", want: "plain"},
		{in: "", want: `""`},
		{in: "a b", want: `"a b"`},
		{in: `a"b`, want: `"a\"b"`},
		{in: `C:\dir\file`, want: `C:\dir\file`},
		{in: `C:\dir with space\`, want: `"C:\dir with space\\"`},
		{in: `a\"b`, want: `"a\\\"b"`},
		{in: `&|<>^%!`, want: `&|<>^%!`},
		{in: "tab\there", want: "\"tab\there\""},
	}
	for _, tt := range tests {
		if got := QuoteArg(tt.in); got != tt.want {
			t.Errorf("QuoteArg(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCommandLine_RoundTripsThroughTheWindowsParser(t *testing.T) {
	program := `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`
	args := []string{"list", "a b", `he said "hi"`, `C:\trailing\`, `C:\space dir\`, "", `\\server\share`, "&|<>^%!", `$(Get-Date)`}
	line, err := CommandLine(program, args...)
	if err != nil {
		t.Fatalf("CommandLine: %v", err)
	}
	gotProgram, gotArgs := parseWindowsArgs(line)
	if gotProgram != program {
		t.Errorf("program = %q, want %q (line %q)", gotProgram, program, line)
	}
	if strings.Join(gotArgs, "\x00") != strings.Join(args, "\x00") || len(gotArgs) != len(args) {
		t.Errorf("args = %q, want %q (line %q)", gotArgs, args, line)
	}
}

// TestParseWindowsArgs_Control proves the model is not trivially
// permissive: a line built by naive joining splits differently from the
// arguments that went in, which is the failure CommandLine prevents.
func TestParseWindowsArgs_Control(t *testing.T) {
	_, got := parseWindowsArgs(`prog a b "c`)
	if len(got) != 3 {
		t.Fatalf("model split %q, want three arguments from a naive join", got)
	}
}

func TestCommandLine_Refusals(t *testing.T) {
	if _, err := CommandLine(""); err == nil {
		t.Error("an empty program was accepted")
	}
	if _, err := CommandLine(`C:\a"b.exe`); err == nil || !strings.Contains(err.Error(), "quote") {
		t.Errorf("a quote in the program position: err = %v", err)
	}
	if _, err := CommandLine("prog", "a\x00b"); err == nil || !strings.Contains(err.Error(), "argument 1") {
		t.Errorf("a NUL argument: err = %v, want it to name argument 1", err)
	}
}

func TestCmdLine(t *testing.T) {
	script := `echo "quoted" & set`
	sent, err := commandLine(ShellCmd, script, Options{})
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	// What the service's cmd.exe /C passes on is exactly the inner cmd.exe
	// invocation, so the script is read once, by the cmd.exe named here.
	inner := DefaultCmdPath + ` /d /v:on /s /c "` + script + `"`
	if back, ok := cmdSlashC(sent, modelEnv); !ok || back != inner {
		t.Errorf("sent %q, which cmd.exe /C turns into %q (ok=%v), want %q", sent, back, ok, inner)
	}
	sent, err = commandLine(ShellCmd, "ver", Options{CmdPath: `D:\Win dows\cmd.exe`})
	if err != nil || !strings.HasPrefix(sent, `""D:\Win dows\cmd.exe" /d /v:on /s /c`) {
		t.Errorf("a configured path with a space: sent %q, err = %v", sent, err)
	}
}

func TestPowerShellLine_DecodesToTheWrappedScript(t *testing.T) {
	script := "Write-Output 'hi'\n$x = 1 # a comment \u00e9 \U0001F600"
	line, err := commandLine(ShellPowerShell, script, Options{})
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	prefix := DefaultPowerShellPath + " -NoLogo -NoProfile -NonInteractive -EncodedCommand "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("line = %q, want prefix %q", line, prefix)
	}
	if got, want := decodePowerShell(t, strings.TrimPrefix(line, prefix)), powerShellPrologue+script+powerShellEpilogue; got != want {
		t.Errorf("decoded = %q, want %q", got, want)
	}
}

// decodePowerShell reverses -EncodedCommand's encoding.
func decodePowerShell(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("odd UTF-16 byte count %d", len(raw))
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

func TestCommandLine_Ceilings(t *testing.T) {
	// Base64 of UTF-16LE is 8/3 of the script's length, so this is well
	// past CreateProcess's limit once encoded.
	if _, err := commandLine(ShellPowerShell, strings.Repeat("x", 4000), Options{}); err == nil || !strings.Contains(err.Error(), "8191") {
		t.Errorf("an oversized PowerShell script: err = %v, want the 8191 limit named", err)
	}
	if _, err := commandLine(ShellNone, "prog "+strings.Repeat("x", MaxCmdLine), Options{}); err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Errorf("an oversized command line: err = %v, want it to point at stdin", err)
	}
	// The limit is measured after escaping: 3000 ampersands become 6000
	// characters, and 5000 go over.
	if _, err := commandLine(ShellNone, "prog "+strings.Repeat("&", 3000), Options{}); err != nil {
		t.Errorf("3000 escaped ampersands: %v", err)
	}
	if _, err := commandLine(ShellNone, "prog "+strings.Repeat("&", 5000), Options{}); err == nil {
		t.Error("5000 ampersands, 10000 characters once escaped, were accepted")
	}
	// A non-BMP character is two UTF-16 units, which is what Windows counts.
	if got := utf16Len("\U0001F600"); got != 2 {
		t.Errorf("utf16Len(emoji) = %d, want 2", got)
	}
}

func TestCheckText(t *testing.T) {
	for _, ok := range []string{"", "tab\tline\nreturn\r", "\u00e9\U0001F600", "\uE000\uFFFD"} {
		if err := CheckText("x", ok); err != nil {
			t.Errorf("CheckText(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"a\x00b", "bell\x07", "\uFFFE", "\xff"} {
		if err := CheckText("x", bad); err == nil {
			t.Errorf("CheckText(%q) accepted text XML cannot carry", bad)
		}
	}
}

// FuzzCommandLine checks, for arbitrary arguments, that the standard
// Windows parser recovers exactly what CommandLine was given.
func FuzzCommandLine(f *testing.F) {
	f.Add("a b", `c"d`, `e\`)
	f.Add("", `\\`, `"`)
	f.Add("&|<>", "%PATH%", "^")
	f.Fuzz(func(t *testing.T, a, b, c string) {
		args := []string{a, b, c}
		for _, arg := range args {
			if CheckText("arg", arg) != nil || strings.ContainsAny(arg, "\n\v") {
				// Refused text, or a vertical whitespace the model does
				// not split on, which QuoteArg quotes but the model would
				// not need to.
				return
			}
		}
		line, err := CommandLine(`C:\p q\prog.exe`, args...)
		if err != nil {
			t.Fatalf("CommandLine(%q): %v", args, err)
		}
		_, got := parseWindowsArgs(line)
		if len(got) != len(args) {
			t.Fatalf("line %q parsed to %q, want %q", line, got, args)
		}
		for i := range args {
			if got[i] != args[i] {
				t.Fatalf("line %q parsed to %q, want %q", line, got, args)
			}
		}
	})
}

// FuzzPowerShellLine checks that any script PowerShell mode accepts
// decodes back to exactly the wrapped script, and that anything it
// cannot carry is refused rather than altered.
func FuzzPowerShellLine(f *testing.F) {
	f.Add("Write-Output 1")
	f.Add("\U0001F600 \x00")
	f.Add("$(Get-Date); `n; ]]>")
	f.Fuzz(func(t *testing.T, script string) {
		line, err := commandLine(ShellPowerShell, script, Options{})
		if CheckText("s", script) != nil {
			if err == nil {
				t.Fatalf("script %q XML cannot carry was accepted", script)
			}
			return
		}
		if err != nil {
			// The only other refusal is length.
			if !strings.Contains(err.Error(), "characters") {
				t.Fatalf("unexpected refusal of %q: %v", script, err)
			}
			return
		}
		encoded := line[strings.LastIndex(line, " ")+1:]
		if got := decodePowerShell(t, encoded); got != powerShellPrologue+script+powerShellEpilogue {
			t.Fatalf("script %q decoded to %q", script, got)
		}
	})
}

// FuzzCmdLine checks, for any script cmd mode accepts, that the service's
// cmd.exe /C passes on exactly the inner cmd.exe invocation carrying the
// script, and that no line is ever sent across two lines.
func FuzzCmdLine(f *testing.F) {
	f.Add(`echo "a" & b`)
	f.Add(`odd " quote ^ %PATH% !OS!`)
	f.Fuzz(func(t *testing.T, script string) {
		sent, err := commandLine(ShellCmd, script, Options{})
		if err != nil {
			return
		}
		inner := DefaultCmdPath + ` /d /v:on /s /c "` + script + `"`
		if back, ok := cmdSlashC(sent, modelEnv); !ok || back != inner {
			t.Fatalf("script %q: cmd.exe /C gives %q (ok=%v), want %q", script, back, ok, inner)
		}
		if strings.ContainsAny(sent, "\r\n") {
			t.Fatalf("line %q spans lines", sent)
		}
	})
}

// TestCheckCmdEnvReads covers the refusal that keeps a cmd script from
// reading one of its own values as %NAME%, which cmd.exe expands before it
// parses the line (measured on Windows 11: a value "a & echo INJECTED"
// ran the echo). Only a read of the command's own variables is refused,
// in either expansion form and in any case; !NAME!, the form that stays
// text, and every variable the command does not set pass.
func TestCheckCmdEnvReads(t *testing.T) {
	env := map[string]string{"TOKEN": "a & echo INJECTED"}
	tests := []struct {
		name    string
		script  string
		env     map[string]string
		refused bool
	}{
		{"read as %NAME%", "echo %TOKEN%", env, true},
		{"read in another case", "echo %token%", env, true},
		{"the substring form", "echo %TOKEN:~0,3%", env, true},
		{"the replace form", "echo %TOKEN:a=b%", env, true},
		{"read as !NAME!", "echo !TOKEN!", env, false},
		{"a variable the command does not set", "echo %PATH%", env, false},
		{"a longer name that starts with it", "echo %TOKENS%", env, false},
		{"no environment at all", "echo %TOKEN%", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkCmdEnvReads(tt.script, tt.env)
			if (err != nil) != tt.refused {
				t.Fatalf("checkCmdEnvReads(%q) = %v, want refused %v", tt.script, err, tt.refused)
			}
			if err != nil && !strings.Contains(err.Error(), "!TOKEN!") {
				t.Errorf("the refusal %q does not say how to read the value instead", err)
			}
		})
	}
}
