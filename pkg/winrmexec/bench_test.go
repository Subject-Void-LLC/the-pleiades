// Benchmarks for the per-command work this package does before anything
// reaches the network: building each mode's command line, escaping it for
// the service's cmd.exe, and building the Command message. Each runs once
// per WinRM command, so each must stay negligible beside a network round
// trip, which is tens of milliseconds even on a local host.
package winrmexec

import (
	"strings"
	"testing"

	"github.com/masterzen/winrm"
)

// benchArgs is an argument vector heavy with everything that needs
// quoting or escaping.
var benchArgs = []string{"a b", "c&d|e", "f^g", "%PATH%", `q"uote`, `C:\trailing\`, "", "!bang!", "(x)", "<y>"}

func BenchmarkCommandLineNone(b *testing.B) {
	for i := 0; i < b.N; i++ {
		line, err := CommandLine(`C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`, benchArgs...)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := commandLine(ShellNone, line, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCommandLinePowerShell(b *testing.B) {
	script := strings.Repeat("Get-ChildItem -Path $env:TEMP | Select-Object -First 5\n", 20)
	for i := 0; i < b.N; i++ {
		if _, err := commandLine(ShellPowerShell, script, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTransparentLine(b *testing.B) {
	line := `"C:\Program Files\x.exe" ` + strings.Repeat(`a&b|c "d" %e% !f! `, 100)
	for i := 0; i < b.N; i++ {
		if _, err := transparentLine(line); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCommandMessage(b *testing.B) {
	line := strings.Repeat(`x^&y ^"z^" `, 200)
	for i := 0; i < b.N; i++ {
		msg := commandMessage("https://h:5986/wsman", *winrm.DefaultParameters, "SHELL", line, false)
		_ = msg.String()
		msg.Free()
	}
}
