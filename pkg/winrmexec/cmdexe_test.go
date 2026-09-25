// Tests for transparentLine, against a Go model of how `cmd.exe /C`
// rewrites the rest of its command line.
//
// The model implements the three steps cmdexe.go names and nothing else.
// It is what lets "cmd.exe passes this through unchanged" be checked as a
// property and fuzzed; the evidence that real cmd.exe agrees is the WinRM
// modes Release Gate, where a program on a real host reports the
// arguments it received.
package winrmexec

import (
	"strings"
	"testing"
)

// modelEnv is the environment the model expands against. Every name in it
// is defined, so a percent sign that escaping missed shows up as an
// expansion.
var modelEnv = map[string]string{"PATH": "EXPANDED", "OS": "EXPANDED", "A": "EXPANDED", "X": "EXPANDED"}

// cmdSlashC models `cmd.exe /C` turning the rest of its command line into
// the command line it starts. ok is false when an unescaped & | < > ( )
// outside quotes would end or redirect the command.
func cmdSlashC(line string, env map[string]string) (string, bool) {
	// Step 1: a leading quote goes, and so does the last quote.
	if strings.HasPrefix(line, `"`) {
		if last := strings.LastIndex(line, `"`); last > 0 {
			line = line[1:last] + line[last+1:]
		} else {
			line = line[1:]
		}
	}
	// Step 2: %NAME% expands when NAME is defined; otherwise the percent
	// sign stays, as it does on a command line (not in a batch file).
	var expanded strings.Builder
	for i := 0; i < len(line); {
		if line[i] == '%' {
			if j := strings.IndexByte(line[i+1:], '%'); j >= 0 {
				if value, ok := env[line[i+1:i+1+j]]; ok {
					expanded.WriteString(value)
					i += j + 2
					continue
				}
			}
		}
		expanded.WriteByte(line[i])
		i++
	}
	line = expanded.String()
	// Step 3: an unescaped quote toggles a quoted run; outside one, a
	// caret makes the next character literal, and a metacharacter ends the
	// command.
	var out strings.Builder
	inQuotes := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"':
			inQuotes = !inQuotes
			out.WriteByte(c)
		case inQuotes:
			out.WriteByte(c)
		case c == '^':
			if i+1 < len(line) {
				i++
				out.WriteByte(line[i])
			}
		case strings.IndexByte("&|<>()", c) >= 0:
			return out.String(), false
		default:
			out.WriteByte(c)
		}
	}
	return out.String(), true
}

func TestTransparentLine(t *testing.T) {
	tests := []struct{ line, want string }{
		{line: `C:\x.exe`, want: `C:\x.exe`},
		{line: `C:\x.exe a&b|c`, want: `C:\x.exe a^&b^|c`},
		{line: `C:\x.exe "a b" %PATH% !x!`, want: `C:\x.exe ^"a b^" ^%PATH^% ^!x^!`},
		{line: `"C:\Program Files\x.exe" (y) <z>`, want: `""C:\Program Files\x.exe" ^(y^) ^<z^>"`},
		{line: `"C:\Program Files (x86)\x.exe"`, want: `""C:\Program Files (x86)\x.exe""`},
	}
	for _, tt := range tests {
		got, err := transparentLine(tt.line)
		if err != nil || got != tt.want {
			t.Errorf("transparentLine(%q) = %q, %v; want %q", tt.line, got, err, tt.want)
		}
		if back, ok := cmdSlashC(got, modelEnv); !ok || back != tt.line {
			t.Errorf("cmd.exe /C of %q gives %q (ok=%v), want the line unchanged", got, back, ok)
		}
	}
}

func TestTransparentLine_Refusals(t *testing.T) {
	for _, line := range []string{
		"", " C:\\x.exe", "\tC:\\x.exe", "C:\\x.exe a\nb", "C:\\x.exe a\rb", `"C:\unclosed.exe a`,
		`"C:\%TEMP%\x.exe"`, `"C:\bang!\x.exe"`, `C:\a&b.exe`, `C:\a,b.exe`, `C:\a=b.exe`,
	} {
		if _, err := transparentLine(line); err == nil {
			t.Errorf("transparentLine(%q) was accepted", line)
		}
	}
}

// TestCmdSlashC_Control proves the model is not the identity: unescaped,
// a metacharacter ends the command, a defined variable expands, and a
// quoted program loses its quotes.
func TestCmdSlashC_Control(t *testing.T) {
	if _, ok := cmdSlashC(`C:\x.exe a&calc.exe`, modelEnv); ok {
		t.Error("an unescaped & did not end the command in the model")
	}
	if got, _ := cmdSlashC(`C:\x.exe %PATH%`, modelEnv); got != `C:\x.exe EXPANDED` {
		t.Errorf("an unescaped %%PATH%% gave %q in the model, want it expanded", got)
	}
	if got, _ := cmdSlashC(`"C:\Program Files\x.exe" a`, modelEnv); got == `"C:\Program Files\x.exe" a` {
		t.Error("a quoted program kept its quotes in the model, so the extra pair would not be needed")
	}
}

// FuzzTransparentLine checks, for any line transparentLine accepts, that
// the modeled cmd.exe /C hands it on exactly as written.
func FuzzTransparentLine(f *testing.F) {
	f.Add(`C:\x.exe a&b|c <d> (e) ^f "g h" %PATH% !OS! %A%%X%`)
	f.Add(`"C:\Program Files\x.exe" "" "\"" ^^ %%`)
	f.Add(`C:\x.exe`)
	f.Fuzz(func(t *testing.T, line string) {
		sent, err := transparentLine(line)
		if err != nil {
			return
		}
		back, ok := cmdSlashC(sent, modelEnv)
		if !ok || back != line {
			t.Fatalf("line %q was sent as %q and cmd.exe /C gives %q (ok=%v)", line, sent, back, ok)
		}
	})
}
