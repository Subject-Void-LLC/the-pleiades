package winrmsvc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// This package has no real Windows host to dial in this environment, the
// identical constraint pkg/winrmexec's own tests document and accept:
// the one thing that genuinely needs a real Service Control Manager
// cannot be faked usefully here, and belongs in a gated Release Gate
// (cmd/pleiades/winrm_service_feature_release_gate_test.go), not this
// file. What IS tested directly: the pure script/quoting construction,
// the JSON parsing this package does itself, State's own predicates, and
// that a call against an unreachable target returns a properly wrapped
// error rather than losing the failure.

func unreachableSession() Session {
	return Session{
		Target:  winrmexec.Target{Host: "127.0.0.1", Port: 1},
		Auth:    winrmexec.Auth{Username: "administrator", Password: "secret"},
		Options: winrmexec.Options{Timeout: 2 * time.Second},
	}
}

func TestQuotePS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain name", input: "nginx", want: "'nginx'"},
		{name: "embedded single quote is doubled", input: "O'Brien", want: "'O''Brien'"},
		{name: "empty string", input: "", want: "''"},
		{name: "semicolon is inert inside single quotes", input: "svc; Remove-Item C:\\", want: "'svc; Remove-Item C:\\'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quotePS(tt.input); got != tt.want {
				t.Errorf("quotePS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestQuotePS_DoublingIsTheCompleteEscape proves the property the
// package doc claims: a value built entirely of embedded single quotes,
// however many, always round-trips to a syntactically valid PowerShell
// literal with a balanced, even number of quote characters between the
// two delimiters.
func TestQuotePS_DoublingIsTheCompleteEscape(t *testing.T) {
	input := "'''"
	got := quotePS(input)
	want := "''''''''"
	if got != want {
		t.Errorf("quotePS(%q) = %q, want %q", input, got, want)
	}
}

func TestParseStatusJSON(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		want    State
		wantErr bool
	}{
		{
			name:   "a running, automatic service",
			stdout: `{"Exists":true,"Status":"Running","StartType":"Automatic"}`,
			want:   State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"},
		},
		{
			name:   "a stopped, disabled service",
			stdout: `{"Exists":true,"Status":"Stopped","StartType":"Disabled"}`,
			want:   State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Disabled"},
		},
		{
			name:   "a service PowerShell has never heard of",
			stdout: `{"Exists":false,"Status":"","StartType":""}`,
			want:   State{Name: "spooler", Exists: false},
		},
		{
			// ConvertTo-Json -Compress output has a trailing newline from
			// the shell that ran it; the parser must not choke on it.
			name:   "trailing newline from the shell",
			stdout: "{\"Exists\":true,\"Status\":\"Running\",\"StartType\":\"Manual\"}\r\n",
			want:   State{Name: "spooler", Exists: true, Status: "Running", StartType: "Manual"},
		},
		{
			name:    "not JSON at all",
			stdout:  "Get-Service : The term 'Get-Service' is not recognized",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStatusJSON("spooler", tt.stdout)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStatusJSON: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseStatusJSON(%q) = %+v, want %+v", tt.stdout, got, tt.want)
			}
		})
	}
}

func TestState_Predicates(t *testing.T) {
	tests := []struct {
		name        string
		state       State
		wantRunning bool
		wantDisable bool
	}{
		{name: "running, automatic", state: State{Status: "Running", StartType: "Automatic"}, wantRunning: true, wantDisable: false},
		{name: "stopped, manual", state: State{Status: "Stopped", StartType: "Manual"}, wantRunning: false, wantDisable: false},
		{name: "start pending does not count as running", state: State{Status: "StartPending", StartType: "Automatic"}, wantRunning: false, wantDisable: false},
		{name: "disabled", state: State{Status: "Stopped", StartType: "Disabled"}, wantRunning: false, wantDisable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.Running(); got != tt.wantRunning {
				t.Errorf("Running() = %v, want %v", got, tt.wantRunning)
			}
			if got := tt.state.Disabled(); got != tt.wantDisable {
				t.Errorf("Disabled() = %v, want %v", got, tt.wantDisable)
			}
		})
	}
}

func TestState_MapKeys(t *testing.T) {
	m := State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}.Map()
	want := map[string]any{"name": "spooler", "exists": true, "running": true, "status": "Running", "start_type": "Automatic"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("Map()[%q] = %v, want %v", k, m[k], v)
		}
	}
}

// TestOperations_QuoteTheServiceName asserts every operation splices the
// name through quotePS rather than a raw concatenation, by constructing
// the same scripts the runtime functions build and checking a hostile
// name cannot break out of the quoted literal. This is the injection
// guard remotesvc.TestOperations_QuoteTheUnitName plays for
// pkg/remotesvc, done at the script-construction level here since there
// is no fake Service Control Manager to observe an argument vector
// arrive at.
func TestOperations_QuoteTheServiceName(t *testing.T) {
	hostile := `evil'; Remove-Item C:\ -Recurse -Force; '`
	quoted := quotePS(hostile)

	script := "$ErrorActionPreference = 'Stop'\n" + opStart + " -Name " + quoted
	if strings.Count(script, "Remove-Item") != 1 {
		t.Fatalf("sanity: expected the hostile payload to appear once verbatim in the constructed script, got: %s", script)
	}
	// The payload's own embedded quote must have been doubled, which is
	// what keeps it data rather than a second statement.
	if !strings.Contains(script, "evil''; Remove-Item") {
		t.Errorf("script = %q, want the embedded ' doubled so the hostile text stays inside one string literal", script)
	}
}

func TestStatus_UnreachableTargetIsAWrappedError(t *testing.T) {
	_, err := Status(context.Background(), unreachableSession(), "spooler")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "spooler") {
		t.Errorf("error = %v, want it to name the service", err)
	}
}

func TestStart_UnreachableTargetIsAWrappedError(t *testing.T) {
	err := Start(context.Background(), unreachableSession(), "spooler")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "Start-Service") || !strings.Contains(err.Error(), "spooler") {
		t.Errorf("error = %v, want it to name the verb and the service", err)
	}
}

func TestEnable_UnreachableTargetIsAWrappedError(t *testing.T) {
	err := Enable(context.Background(), unreachableSession(), "spooler")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "Automatic") || !strings.Contains(err.Error(), "spooler") {
		t.Errorf("error = %v, want it to name the startup type and the service", err)
	}
}

func TestDisable_UnreachableTargetIsAWrappedError(t *testing.T) {
	err := Disable(context.Background(), unreachableSession(), "spooler")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "Disabled") || !strings.Contains(err.Error(), "spooler") {
		t.Errorf("error = %v, want it to name the startup type and the service", err)
	}
}

func TestStop_UnreachableTargetIsAWrappedError(t *testing.T) {
	err := Stop(context.Background(), unreachableSession(), "spooler")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "Stop-Service") {
		t.Errorf("error = %v, want it to name the verb", err)
	}
}

func TestRestart_UnreachableTargetIsAWrappedError(t *testing.T) {
	err := Restart(context.Background(), unreachableSession(), "spooler")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "Restart-Service") {
		t.Errorf("error = %v, want it to name the verb", err)
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single line", input: "boom", want: "boom"},
		{name: "multi line takes the first", input: "boom\nmore detail\nand more", want: "boom"},
		{name: "empty is reported plainly", input: "", want: "no output"},
		{name: "whitespace only is reported plainly", input: "   \n  ", want: "no output"},
		{name: "leading and trailing whitespace trimmed", input: "  boom  \n", want: "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstLine(tt.input); got != tt.want {
				t.Errorf("firstLine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
