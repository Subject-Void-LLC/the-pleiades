package transport_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// TestParseShell_Valid covers every token a runbook may write, plus the
// two forms of "the author did not choose one".
//
// The empty string mapping to ShellNone is the load-bearing case: it is
// what makes ShellNone the zero value in practice as well as in the type,
// so a task that never mentions a shell keeps the meaning it had before
// shells could be named.
func TestParseShell_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  transport.Shell
	}{
		{name: "omitted", input: "", want: transport.ShellNone},
		{name: "whitespace only", input: "   ", want: transport.ShellNone},
		{name: "none", input: "none", want: transport.ShellNone},
		{name: "cmd", input: "cmd", want: transport.ShellCmd},
		{name: "powershell", input: "powershell", want: transport.ShellPowerShell},
		{name: "mixed case", input: "PowerShell", want: transport.ShellPowerShell},
		{name: "surrounding whitespace", input: "  cmd\n", want: transport.ShellCmd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := transport.ParseShell(tt.input)
			if err != nil {
				t.Fatalf("ParseShell(%q): unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseShell(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseShell_UnknownListsValidValues checks the error's content, not
// just that one occurred.
//
// An author reaching this message picked a word this platform does not
// know, and the only useful reply is the set it does know. A bare
// "invalid shell" would send them to the source to find out.
func TestParseShell_UnknownListsValidValues(t *testing.T) {
	_, err := transport.ParseShell("bash")
	if err == nil {
		t.Fatal("expected an error for an unknown shell")
	}
	for _, want := range []string{"bash", "none", "cmd", "powershell"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// TestParseShell_RejectsSubstringMatches guards the specific mistake a
// looser implementation would make: matching a token because a valid one
// appears inside it. "powershell-core" is not "powershell", and quietly
// accepting it would run the task through an interpreter the author did
// not name.
func TestParseShell_RejectsSubstringMatches(t *testing.T) {
	for _, input := range []string{"powershell-core", "cmder", "nonesuch", "pwsh"} {
		if _, err := transport.ParseShell(input); err == nil {
			t.Errorf("ParseShell(%q) succeeded, want an error", input)
		}
	}
}

// TestShell_StringRoundTrips proves String and ParseShell share one
// vocabulary. They read from the same table so they cannot drift, and
// this is what would catch it if someone gave either its own copy.
func TestShell_StringRoundTrips(t *testing.T) {
	for _, shell := range []transport.Shell{transport.ShellNone, transport.ShellCmd, transport.ShellPowerShell} {
		parsed, err := transport.ParseShell(shell.String())
		if err != nil {
			t.Errorf("ParseShell(%q): unexpected error: %v", shell.String(), err)
			continue
		}
		if parsed != shell {
			t.Errorf("ParseShell(%q) = %v, want %v", shell.String(), parsed, shell)
		}
	}
}

// TestShell_StringUnknown proves an out-of-range Shell still prints
// something a reader can act on. A bare "" here would turn a
// programming error into an error message with a hole in it.
func TestShell_StringUnknown(t *testing.T) {
	got := transport.Shell(99).String()
	if !strings.Contains(got, "99") {
		t.Errorf("Shell(99).String() = %q, want it to include the numeric value", got)
	}
}
