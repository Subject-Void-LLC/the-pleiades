// Tests for the Shell enum a runbook's params.shell names.
package transport_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

func TestParseShell(t *testing.T) {
	tests := []struct {
		token string
		want  transport.Shell
	}{
		{token: "", want: transport.ShellNone},
		{token: "none", want: transport.ShellNone},
		{token: " CMD ", want: transport.ShellCmd},
		{token: "PowerShell", want: transport.ShellPowerShell},
	}
	for _, tt := range tests {
		got, err := transport.ParseShell(tt.token)
		if err != nil || got != tt.want {
			t.Errorf("ParseShell(%q) = %v, %v; want %v", tt.token, got, err, tt.want)
		}
	}
	for _, bad := range []string{"bash", "pwsh", "cmder", "powershell-core"} {
		_, err := transport.ParseShell(bad)
		if err == nil || !strings.Contains(err.Error(), "none, cmd, powershell") {
			t.Errorf("ParseShell(%q) err = %v, want the valid values listed", bad, err)
		}
	}
}

func TestShell_StringRoundTrips(t *testing.T) {
	for _, s := range []transport.Shell{transport.ShellNone, transport.ShellCmd, transport.ShellPowerShell} {
		if got, err := transport.ParseShell(s.String()); err != nil || got != s {
			t.Errorf("round trip of %v = %v, %v", s, got, err)
		}
	}
	if got := transport.Shell(9).String(); !strings.Contains(got, "9") {
		t.Errorf("Shell(9).String() = %q", got)
	}
}
