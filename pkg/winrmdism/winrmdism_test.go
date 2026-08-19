package winrmdism

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// See pkg/winrmsvc's own test file doc comment for why there is no live
// Windows host here and what that does and does not mean for coverage.

const testLogPath = `C:\Windows\Logs\DISM\dism.log`

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
		{name: "plain feature name", input: "IIS-WebServerRole", want: "'IIS-WebServerRole'"},
		{name: "embedded single quote is doubled", input: "O'Brien", want: "'O''Brien'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quotePS(tt.input); got != tt.want {
				t.Errorf("quotePS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDismCommand_Status(t *testing.T) {
	script := dismCommand(testLogPath, "/get-featureinfo", "/featurename:"+quotePS("IIS-WebServerRole"))

	for _, want := range []string{
		"dism.exe", "/online", "/get-featureinfo", "/featurename:'IIS-WebServerRole'",
		"/logpath:" + quotePS(testLogPath), "exit $LASTEXITCODE",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script = %q, want it to contain %q", script, want)
		}
	}
}

// TestDismCommand_EnableIncludesAllDisableDoesNot pins the one asymmetry
// this package's package doc commits to: enabling a feature also pulls
// in its required parent features, and disabling one does not cascade
// to the parents it depended on.
func TestDismCommand_EnableIncludesAllDisableDoesNot(t *testing.T) {
	enableScript := dismCommand(testLogPath, "/enable-feature", "/featurename:"+quotePS("Foo"), "/all", "/norestart")
	if !strings.Contains(enableScript, "/all") {
		t.Errorf("enable script = %q, want it to contain /all", enableScript)
	}

	disableScript := dismCommand(testLogPath, "/disable-feature", "/featurename:"+quotePS("Foo"), "/norestart")
	if strings.Contains(disableScript, "/all") {
		t.Errorf("disable script = %q, want it to NOT contain /all", disableScript)
	}
}

func TestParseStateLine(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   string
		wantOK bool
	}{
		{
			name: "a real /get-featureinfo transcript",
			stdout: "Deployment Image Servicing and Management tool\r\n" +
				"Version: 10.0.20348.1\r\n\r\n" +
				"Image Version: 10.0.20348.1\r\n\r\n" +
				"Features listing for package :\r\n\r\n" +
				"Feature Name : IIS-WebServerRole\r\n\r\n" +
				"State : Enabled\r\n\r\n" +
				"Display Name : Web Server (IIS)\r\n\r\n" +
				"Description : Web Server\r\n\r\n" +
				"Restart Required : Possible\r\n\r\n" +
				"Feature Type : Feature\r\n\r\n" +
				"The operation completed successfully.\r\n",
			want:   "Enabled",
			wantOK: true,
		},
		{
			name:   "disabled state",
			stdout: "Feature Name : IIS-WebServerRole\r\n\r\nState : Disabled\r\n\r\n",
			want:   "Disabled",
			wantOK: true,
		},
		{
			name:   "no State line at all",
			stdout: "Feature Name : IIS-WebServerRole\r\n\r\nDisplay Name : Web Server (IIS)\r\n\r\n",
			wantOK: false,
		},
		{
			name:   "empty output",
			stdout: "",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseStateLine(tt.stdout)
			if ok != tt.wantOK {
				t.Fatalf("parseStateLine ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("parseStateLine = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFeatureState_Predicates(t *testing.T) {
	tests := []struct {
		name         string
		state        FeatureState
		wantEnabled  bool
		wantDisabled bool
	}{
		{name: "enabled", state: FeatureState{State: "Enabled"}, wantEnabled: true, wantDisabled: false},
		{name: "disabled", state: FeatureState{State: "Disabled"}, wantEnabled: false, wantDisabled: true},
		{name: "enable pending does not count as enabled", state: FeatureState{State: "Enable Pending"}, wantEnabled: false, wantDisabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.Enabled(); got != tt.wantEnabled {
				t.Errorf("Enabled() = %v, want %v", got, tt.wantEnabled)
			}
			if got := tt.state.DisabledState(); got != tt.wantDisabled {
				t.Errorf("DisabledState() = %v, want %v", got, tt.wantDisabled)
			}
		})
	}
}

func TestFeatureState_MapKeys(t *testing.T) {
	m := FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}.Map()
	want := map[string]any{"name": "IIS-WebServerRole", "exists": true, "state": "Enabled"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("Map()[%q] = %v, want %v", k, m[k], v)
		}
	}
}

func TestDismOutput(t *testing.T) {
	tests := []struct {
		name   string
		result winrmexec.Result
		want   []string
	}{
		{
			name:   "stdout only",
			result: winrmexec.Result{Stdout: "Error: 87\r\n\r\nThe Get-FeatureInfo option requires a valid feature name."},
			want:   []string{"stdout:", "Error: 87"},
		},
		{
			name:   "both streams",
			result: winrmexec.Result{Stdout: "some output", Stderr: "some warning"},
			want:   []string{"stdout: some output", "stderr: some warning"},
		},
		{
			name:   "neither stream has anything",
			result: winrmexec.Result{},
			want:   []string{"no output"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dismOutput(tt.result)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("dismOutput() = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

func TestStatus_UnreachableTargetIsAWrappedError(t *testing.T) {
	_, err := Status(context.Background(), unreachableSession(), testLogPath, "IIS-WebServerRole")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "IIS-WebServerRole") {
		t.Errorf("error = %v, want it to name the feature", err)
	}
}

func TestEnable_UnreachableTargetIsAWrappedError(t *testing.T) {
	_, err := Enable(context.Background(), unreachableSession(), testLogPath, "IIS-WebServerRole")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "enable-feature") || !strings.Contains(err.Error(), "IIS-WebServerRole") {
		t.Errorf("error = %v, want it to name the verb and the feature", err)
	}
}

func TestDisable_UnreachableTargetIsAWrappedError(t *testing.T) {
	_, err := Disable(context.Background(), unreachableSession(), testLogPath, "IIS-WebServerRole")
	if err == nil {
		t.Fatal("expected an error dialing an unreachable target")
	}
	if !strings.Contains(err.Error(), "disable-feature") {
		t.Errorf("error = %v, want it to name the verb", err)
	}
}
